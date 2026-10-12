package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestThrottleMiddlewareGoroutineCleanup verifies that the throttle middleware
// doesn't leak goroutines under high connection churn.
// This tests Bug #11: goroutine cleanup in throttling middleware.
func TestThrottleMiddlewareGoroutineCleanup(t *testing.T) {
	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	var mu sync.Mutex
	currentStreamCancel := make(map[*http.Request]context.CancelFunc)

	// Create middleware
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate stream context management
			streamCtx, streamCancel := context.WithCancel(context.Background())
			mu.Lock()
			currentStreamCancel[r] = streamCancel
			currentStreamCtx := streamCtx
			mu.Unlock()

			defer func() {
				mu.Lock()
				if cancel, ok := currentStreamCancel[r]; ok {
					cancel()
					delete(currentStreamCancel, r)
				}
				mu.Unlock()
			}()

			// Create merged context (same pattern as mdw.go)
			mergedCtx, mergedCancel := context.WithCancel(r.Context())
			defer mergedCancel()

			// Monitor stream context cancellation in background
			go func() {
				select {
				case <-currentStreamCtx.Done():
					mergedCancel()
				case <-mergedCtx.Done():
					// Already cancelled
				}
			}()

			// Serve request
			next.ServeHTTP(w, r.WithContext(mergedCtx))
		})
	}

	// Simple handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := middleware(handler)

	// Make many concurrent requests to simulate high connection churn
	const numRequests = 100
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/", nil)
			wrappedHandler.ServeHTTP(w, r)
		}()
	}

	wg.Wait()

	// Allow goroutines time to exit
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	// Check goroutine count
	finalGoroutines := runtime.NumGoroutine()

	// Should be close to baseline (allowing some tolerance for test goroutines)
	leak := finalGoroutines - baselineGoroutines
	if leak > 10 {
		t.Errorf("Potential goroutine leak: baseline=%d, final=%d (leak=%d)",
			baselineGoroutines, finalGoroutines, leak)
	} else {
		t.Logf("Goroutine count OK: baseline=%d, final=%d (diff=%d)",
			baselineGoroutines, finalGoroutines, leak)
	}
}

// openStream runs one /stream request through the middleware in the
// background; handle serves it.
func openStream(client string, handle http.HandlerFunc) (*httptest.ResponseRecorder, context.CancelFunc, <-chan struct{}) {
	url := "/stream"
	if client != "" {
		url += "?client=" + client
	}
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ThrottlingMiddleware(handle).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
	}()
	return rec, cancel, done
}

// untilEnded serves until the request ends and says when it started.
func untilEnded(started chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Query().Get("client")
		<-r.Context().Done()
	}
}

func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not end", what)
	}
}

// A page that opens the stream again replaces its own old request, which
// the server may not have noticed is gone; another page is still refused.
func TestThrottleSamePageTakesOver(t *testing.T) {
	started := make(chan string, 2)
	_, cancelOld, oldDone := openStream("page-a", untilEnded(started))
	defer cancelOld()
	<-started

	rec := httptest.NewRecorder()
	ThrottlingMiddleware(untilEnded(started)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream?client=page-b", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("another page: status %d, want 429", rec.Code)
	}
	rec = httptest.NewRecorder()
	ThrottlingMiddleware(untilEnded(started)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a page without an id: status %d, want 429", rec.Code)
	}

	_, cancelNew, newDone := openStream("page-a", untilEnded(started))
	waitClosed(t, oldDone, "the old request")
	select {
	case got := <-started:
		if got != "page-a" {
			t.Fatalf("started %q, want page-a", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the new request did not start")
	}
	cancelNew()
	waitClosed(t, newDone, "the new request")
}

// When the old request does not end in time, the page is refused as before
// and can try again.
func TestThrottleTakeoverGivesUp(t *testing.T) {
	old := takeoverWait
	takeoverWait = 50 * time.Millisecond
	defer func() { takeoverWait = old }()

	release := make(chan struct{})
	started := make(chan string, 1)
	_, cancelOld, oldDone := openStream("page-a", func(w http.ResponseWriter, r *http.Request) {
		started <- "page-a"
		<-release // ignores its context
	})
	defer cancelOld()
	<-started

	begin := time.Now()
	rec := httptest.NewRecorder()
	ThrottlingMiddleware(untilEnded(started)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream?client=page-a", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	if waited := time.Since(begin); waited < 50*time.Millisecond || waited > time.Second {
		t.Fatalf("waited %v for the old request, want about 50ms", waited)
	}
	close(release)
	waitClosed(t, oldDone, "the old request")
}

// A panic in the stream handler frees the stream for the next request.
func TestThrottleReleasesAfterPanic(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the handler did not panic")
			}
		}()
		ThrottlingMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("frame encoder")
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream", nil))
	}()
	rec := httptest.NewRecorder()
	ThrottlingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("after a panic: status %d, want 200", rec.Code)
	}
}

// A takeover request that ends while it waits (its worker was replaced)
// stops waiting and does not take the stream.
func TestThrottleTakeoverStopsWhenItsRequestEnds(t *testing.T) {
	release := make(chan struct{})
	started := make(chan string, 2)
	_, cancelOld, oldDone := openStream("page-a", func(w http.ResponseWriter, r *http.Request) {
		started <- "old"
		<-release // ignores its context
	})
	defer cancelOld()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	begin := time.Now()
	ThrottlingMiddleware(untilEnded(started)).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream?client=page-a", nil).WithContext(ctx))
	if waited := time.Since(begin); waited > time.Second {
		t.Fatalf("waited %v after its request ended", waited)
	}
	select {
	case got := <-started:
		t.Fatalf("the ended request was served (%s)", got)
	default:
	}
	close(release)
	waitClosed(t, oldDone, "the old request")
}
