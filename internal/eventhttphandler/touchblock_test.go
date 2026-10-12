package eventhttphandler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/pubsub"
)

// fakeGrab records what the block asked of the touch panel.
type fakeGrab struct {
	mu    sync.Mutex
	calls []bool
	fail  error
}

func (g *fakeGrab) grab(on bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if on && g.fail != nil {
		return g.fail
	}
	g.calls = append(g.calls, on)
	return nil
}

func (g *fakeGrab) seen() []bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]bool(nil), g.calls...)
}

// The panel is taken for the first holder and given back by the last.
func TestTouchBlockHoldsWhileAnyoneAsks(t *testing.T) {
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	b.release()
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("with one holder left: %v, want [true]", got)
	}
	b.release()
	b.release() // one release too many changes nothing
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("after the last holder: %v, want [true false]", got)
	}
}

// A grab that fails holds nothing, so the next request tries again.
func TestTouchBlockFailedGrabHoldsNothing(t *testing.T) {
	g := &fakeGrab{fail: errors.New("busy")}
	b := &touchBlock{grab: g.grab}
	if err := b.acquire(); err == nil {
		t.Fatal("acquire succeeded although the grab failed")
	}
	g.fail = nil
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("got %v, want [true]", got)
	}
	if err := (&touchBlock{}).acquire(); err == nil {
		t.Fatal("acquire succeeded without a way to grab")
	}
}

// openGestures runs one /gestures request until stop is called.
func openGestures(t *testing.T, grab func(bool) error, url string) (w *streamWriter, stop func()) {
	t.Helper()
	w = &streamWriter{header: http.Header{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewGestureHandler(pubsub.NewPubSub(), moveGeometry, grab).ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
		close(done)
	}()
	return w, func() {
		cancel()
		<-done
	}
}

func waitFor(t *testing.T, w *streamWriter, want string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if strings.Contains(w.String(), want) {
			return
		}
	}
	t.Fatalf("response lacks %s:\n%s", want, w.String())
}

// ?block=1 takes the panel for as long as the request lasts and says so
// first; a request without it neither asks nor answers.
func TestGestureHandlerBlocksTouchWhileOpen(t *testing.T) {
	g := &fakeGrab{}
	w, stop := openGestures(t, g.grab, "/gestures?rotation=90&block=1")
	waitFor(t, w, `{"touchBlocked":true}`)
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("while open: %v, want [true]", got)
	}
	stop()
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("after closing: %v, want [true false]", got)
	}

	w, stop = openGestures(t, g.grab, "/gestures?rotation=90")
	time.Sleep(50 * time.Millisecond)
	stop()
	if w.String() != "" || len(g.seen()) != 2 {
		t.Fatalf("without block=1: wrote %q, grabs %v", w.String(), g.seen())
	}
}

func TestGestureHandlerReportsFailedBlock(t *testing.T) {
	g := &fakeGrab{fail: errors.New("inappropriate ioctl for device")}
	w, stop := openGestures(t, g.grab, "/gestures?block=1")
	waitFor(t, w, `{"touchBlocked":false,"error":"inappropriate ioctl for device"}`)
	stop()
	if got := g.seen(); len(got) != 0 {
		t.Fatalf("released a block it never held: %v", got)
	}
}
