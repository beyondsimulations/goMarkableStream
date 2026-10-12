package stream

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/debug"
)

var (
	activeWriters int
	maxWriters    = 1 // Maximum allowed writers
	mu            sync.Mutex
	cond          = sync.NewCond(&mu)

	// Stream cancellation context
	streamCtx    context.Context
	streamCancel context.CancelFunc

	// Callback invoked when activeWriters drops to 0
	onIdleCallback func()

	// The browser page holding the stream (its ?client= id, "" if none) and
	// how to end its request. A page that opens the stream again after a
	// lost connection replaces its own old request, which the server may
	// not have noticed is gone, instead of being refused.
	holderClient string
	holderCancel func()
	// reservedFor keeps the stream for a page taking over its own old
	// request, so another page cannot slip in while the old one ends.
	reservedFor string

	// takeoverWait bounds how long a page waits for its old request to end.
	takeoverWait = 5 * time.Second
)

// SetOnIdleCallback sets a callback to be invoked when all streaming connections close.
// The callback is called while holding the mutex, so it should be fast and non-blocking.
func SetOnIdleCallback(cb func()) {
	mu.Lock()
	defer mu.Unlock()
	onIdleCallback = cb
}

func init() {
	streamCtx, streamCancel = context.WithCancel(context.Background())
}

// CancelActiveStreams cancels all active stream connections
// and creates a fresh context for new connections
func CancelActiveStreams() {
	mu.Lock()
	defer mu.Unlock()

	// Cancel existing streams, failing a write stuck on a dead connection
	if streamCancel != nil {
		streamCancel()
	}
	if holderCancel != nil {
		holderCancel()
	}

	// Create new context for future streams
	streamCtx, streamCancel = context.WithCancel(context.Background())
}

// ThrottlingMiddleware to allow new connections only if there are no active writers or if max writers is exceeded.
// A request from the page that holds the stream (same ?client= id) ends the
// old request and takes its place.
func ThrottlingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := r.URL.Query().Get("client")
		mu.Lock()
		debug.Log("Throttle: connection from %s, activeWriters=%d", r.RemoteAddr, activeWriters)
		if activeWriters >= maxWriters && client != "" && client == holderClient && holderCancel != nil && reservedFor == "" {
			log.Printf("Stream: %s opened the stream again, ending its previous connection", r.RemoteAddr)
			reservedFor = client
			holderCancel()
			waitForWriters(r.Context(), takeoverWait)
			reservedFor = ""
		}
		if r.Context().Err() != nil {
			mu.Unlock()
			return // gave up waiting
		}
		if activeWriters >= maxWriters || (reservedFor != "" && reservedFor != client) {
			mu.Unlock()
			debug.Log("Throttle: too many requests, rejecting (%s)", r.RemoteAddr)
			// If too many requests, send a 429 status code
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		// Capture current stream context while holding lock
		currentStreamCtx := streamCtx
		activeWriters++
		debug.Log("Throttle: proceeding with request (%s)", r.RemoteAddr)

		// Create merged context: cancels if client disconnects OR server cancels
		mergedCtx, mergedCancel := context.WithCancel(r.Context())
		defer mergedCancel()

		// Taking over also unblocks a write stuck on a dead connection.
		rc := http.NewResponseController(w)
		holderClient = client
		holderCancel = func() {
			mergedCancel()
			_ = rc.SetWriteDeadline(time.Now())
		}
		mu.Unlock()

		// Deferred, so a panic in the handler cannot keep the stream taken.
		defer func() {
			mu.Lock()
			activeWriters--
			holderClient, holderCancel = "", nil
			debug.Log("Throttle: request completed, activeWriters=%d (%s)", activeWriters, r.RemoteAddr)
			// Not between a page's old request and its new one: the new one
			// would allocate again what the callback frees.
			if activeWriters == 0 && reservedFor == "" && onIdleCallback != nil {
				onIdleCallback()
			}
			cond.Broadcast() // Notify waiting goroutines
			mu.Unlock()
		}()

		// Monitor stream context cancellation in background
		go func() {
			select {
			case <-currentStreamCtx.Done():
				mergedCancel()
			case <-mergedCtx.Done():
				// Already cancelled
			}
		}()

		// Create new request with merged context
		r = r.WithContext(mergedCtx)

		next.ServeHTTP(w, r) // Serve the request
	})
}

// waitForWriters waits, with mu held, until the stream is free, limit has
// passed or the waiting request has ended.
func waitForWriters(ctx context.Context, limit time.Duration) {
	deadline := time.Now().Add(limit)
	broadcast := func() {
		mu.Lock()
		cond.Broadcast()
		mu.Unlock()
	}
	wake := time.AfterFunc(limit, broadcast)
	defer wake.Stop()
	stopOnEnd := context.AfterFunc(ctx, broadcast)
	defer stopOnEnd()
	for activeWriters >= maxWriters && time.Now().Before(deadline) && ctx.Err() == nil {
		cond.Wait()
	}
}
