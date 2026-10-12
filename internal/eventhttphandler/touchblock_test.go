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

// shortTimes makes the lease and the grace short for one test.
func shortTimes(t *testing.T, lease, grace time.Duration) {
	t.Helper()
	oldLease, oldGrace := blockLease, blockGrace
	blockLease, blockGrace = lease, grace
	t.Cleanup(func() { blockLease, blockGrace = oldLease, oldGrace })
}

// waitGrabs waits until the grab calls are want.
func waitGrabs(t *testing.T, g *fakeGrab, want []bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if reflect.DeepEqual(g.seen(), want) {
			return
		}
	}
	t.Fatalf("grabs %v, want %v", g.seen(), want)
}

// The panel is taken for the first holder and given back after the last,
// once the grace has passed.
func TestTouchBlockHoldsWhileAnyoneAsks(t *testing.T) {
	shortTimes(t, time.Minute, 20*time.Millisecond)
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	b.release()
	time.Sleep(50 * time.Millisecond)
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("with one holder left: %v, want [true]", got)
	}
	b.release()
	b.release() // one release too many changes nothing
	waitGrabs(t, g, []bool{true, false})
}

// A holder that asks again within the grace (the browser renewing its
// lease) keeps the panel without giving it back in between.
func TestTouchBlockRenewalKeepsThePanel(t *testing.T) {
	shortTimes(t, time.Minute, 100*time.Millisecond)
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	for i := 0; i < 3; i++ {
		if err := b.acquire(); err != nil {
			t.Fatal(err)
		}
		b.release()
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("while renewing: %v, want [true]", got)
	}
	waitGrabs(t, g, []bool{true, false})
}

// A failed give-back still counts as given back (EVIOCGRAB(0) fails only
// when the panel is not ours), so the next request takes it again.
func TestTouchBlockFailedGiveBack(t *testing.T) {
	shortTimes(t, time.Minute, time.Millisecond)
	var calls []bool
	b := &touchBlock{grab: func(on bool) error {
		calls = append(calls, on)
		if !on {
			return errors.New("invalid argument")
		}
		return nil
	}}
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	b.release()
	time.Sleep(30 * time.Millisecond)
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !reflect.DeepEqual(calls, []bool{true, false, true}) {
		t.Fatalf("grabs %v, want [true false true]", calls)
	}
}

// A refusal (Funnel) applies to joining a block that is already held, too.
func TestTouchBlockRefusal(t *testing.T) {
	g := &fakeGrab{}
	var refuse error
	b := &touchBlock{grab: g.grab, allow: func() error { return refuse }}
	if err := b.acquire(); err != nil {
		t.Fatal(err)
	}
	refuse = errors.New("shared publicly")
	if err := b.acquire(); err == nil {
		t.Fatal("joined a block while it is refused")
	}
	if b.users != 1 {
		t.Fatalf("holders %d, want 1", b.users)
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
func openGestures(t *testing.T, grab func(bool) error, url string) (*streamWriter, func()) {
	t.Helper()
	w, _, stop := openGesturesDone(t, grab, url)
	return w, stop
}

// openGesturesDone is openGestures that also reports when the handler ends
// by itself.
func openGesturesDone(t *testing.T, grab func(bool) error, url string) (*streamWriter, <-chan struct{}, func()) {
	t.Helper()
	w := &streamWriter{header: http.Header{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewGestureHandler(pubsub.NewPubSub(), moveGeometry, grab, nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
		close(done)
	}()
	return w, done, func() {
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
	shortTimes(t, time.Minute, time.Millisecond)
	g := &fakeGrab{}
	w, stop := openGestures(t, g.grab, "/gestures?rotation=90&block=1")
	waitFor(t, w, `{"touchBlocked":true}`)
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("while open: %v, want [true]", got)
	}
	stop()
	waitGrabs(t, g, []bool{true, false})

	w, stop = openGestures(t, g.grab, "/gestures?rotation=90")
	time.Sleep(50 * time.Millisecond)
	stop()
	if w.String() != "" || len(g.seen()) != 2 {
		t.Fatalf("without block=1: wrote %q, grabs %v", w.String(), g.seen())
	}
}

// A blocking request ends by itself after the lease, so a page that went
// away without closing its connection lets go; one without block=1 stays.
func TestGestureHandlerLeaseEndsRequest(t *testing.T) {
	shortTimes(t, 50*time.Millisecond, time.Millisecond)
	g := &fakeGrab{}
	_, done, stop := openGesturesDone(t, g.grab, "/gestures?block=1")
	defer stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the blocking request outlived its lease")
	}
	waitGrabs(t, g, []bool{true, false})

	_, done, stop2 := openGesturesDone(t, g.grab, "/gestures")
	defer stop2()
	select {
	case <-done:
		t.Fatal("a request without block=1 ended by itself")
	case <-time.After(200 * time.Millisecond):
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
