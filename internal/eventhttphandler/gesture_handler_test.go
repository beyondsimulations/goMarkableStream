package eventhttphandler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/pubsub"
	"github.com/owulveryck/goMarkableStream/internal/remarkable"
)

// streamWriter collects a streaming response while the handler still runs.
type streamWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
}

func (w *streamWriter) Header() http.Header { return w.header }
func (w *streamWriter) WriteHeader(int)     {}
func (w *streamWriter) Flush()              {}
func (w *streamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}
func (w *streamWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

// serveUntil runs the handler and replays one gesture until the response
// holds want: the handler subscribes asynchronously, so an early replay can
// be lost.
func serveUntil(t *testing.T, geometry *remarkable.TouchGeometry, url string, gesture []events.InputEventFromSource, want string) string {
	t.Helper()
	bus := pubsub.NewPubSub()
	w := &streamWriter{header: http.Header{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewGestureHandler(bus, geometry, nil, nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		for _, ev := range gesture {
			bus.Publish(ev)
		}
		time.Sleep(gestureMaxInterval + 100*time.Millisecond)
		if strings.Contains(w.String(), want) {
			return w.String()
		}
	}
	t.Fatalf("response lacks %s:\n%s", want, w.String())
	return ""
}

// On the Paper Pro and the Move the handler names the swipe for the view:
// a finger moving toward the page's bottom edge is a right-to-left swipe
// when the browser turns the page 90 degrees.
func TestGestureHandlerNamesSwipeForView(t *testing.T) {
	gesture := []events.InputEventFromSource{touchAbs(codeTrackingID, 3)}
	for y := int32(300); y <= 1500; y += 100 {
		gesture = append(gesture, touchAbs(53, 600), touchAbs(54, y))
	}
	gesture = append(gesture, touchAbs(codeTrackingID, -1))

	serveUntil(t, moveGeometry, "/gestures?rotation=90", gesture, `{"swipe":"swipe-left"}`)
	serveUntil(t, moveGeometry, "/gestures", gesture, `{"swipe":"swipe-down"}`)
}

// Without a geometry (reMarkable 2) it keeps sending the direction totals.
func TestGestureHandlerKeepsTotalsWithoutGeometry(t *testing.T) {
	serveUntil(t, nil, "/gestures", swipe(1000, 500, 60, 0, 10), `"left":600`)
}
