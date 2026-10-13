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

// mustAcquire takes a hold for the page or fails the test.
func mustAcquire(t *testing.T, b *touchBlock, client string) uint64 {
	t.Helper()
	id, err := b.acquire(client)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The panel is taken for the first holder and given back after the last,
// once the grace has passed.
func TestTouchBlockHoldsWhileAnyoneAsks(t *testing.T) {
	shortTimes(t, time.Minute, 20*time.Millisecond)
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	first := mustAcquire(t, b, "page-a")
	second := mustAcquire(t, b, "page-a")
	b.release(first)
	time.Sleep(50 * time.Millisecond)
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("with one holder left: %v, want [true]", got)
	}
	b.release(second)
	b.release(second) // one release too many changes nothing
	waitGrabs(t, g, []bool{true, false})
}

// A holder that asks again within the grace (the browser renewing its
// lease) keeps the panel without giving it back in between.
func TestTouchBlockRenewalKeepsThePanel(t *testing.T) {
	shortTimes(t, time.Minute, 100*time.Millisecond)
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	for i := 0; i < 3; i++ {
		b.release(mustAcquire(t, b, "page-a"))
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
	b.release(mustAcquire(t, b, "page-a"))
	time.Sleep(30 * time.Millisecond)
	mustAcquire(t, b, "page-a")
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
	mustAcquire(t, b, "page-a")
	refuse = errors.New("shared publicly")
	if _, err := b.acquire("page-a"); err == nil {
		t.Fatal("joined a block while it is refused")
	}
	if len(b.holds) != 1 {
		t.Fatalf("holds %v, want one", b.holds)
	}
}

// A grab that fails holds nothing, so the next request tries again.
func TestTouchBlockFailedGrabHoldsNothing(t *testing.T) {
	g := &fakeGrab{fail: errors.New("busy")}
	b := &touchBlock{grab: g.grab}
	if _, err := b.acquire("page-a"); err == nil {
		t.Fatal("acquire succeeded although the grab failed")
	}
	g.fail = nil
	mustAcquire(t, b, "page-a")
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("got %v, want [true]", got)
	}
	if _, err := (&touchBlock{}).acquire("page-a"); err == nil {
		t.Fatal("acquire succeeded without a way to grab")
	}
}

// Turning the block off gives the panel back at once, unless another page
// holds it; the page's request that ends later changes nothing.
func TestTouchBlockDrop(t *testing.T) {
	shortTimes(t, time.Minute, time.Hour) // the grace never runs out here
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	a := mustAcquire(t, b, "page-a")
	bb := mustAcquire(t, b, "page-b")
	if !b.drop("page-a") {
		t.Fatal("page-b still holds the panel, but drop reported no one")
	}
	b.release(a) // page-a's old request ends
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("with page-b holding: %v, want [true]", got)
	}
	if b.drop("page-b") {
		t.Fatal("no page holds the panel, but drop reported one")
	}
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("after the last drop: %v, want [true false] at once", got)
	}
	b.release(bb)
	if len(g.seen()) != 2 {
		t.Fatalf("a dropped request's release grabbed again: %v", g.seen())
	}
}

// Off and on again quickly: the old request, ended by drop, returns only
// after the new one took its hold, and must not end the new hold.
func TestTouchBlockOldReleaseKeepsNewHold(t *testing.T) {
	shortTimes(t, time.Minute, 20*time.Millisecond)
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	old := mustAcquire(t, b, "page-a")
	b.drop("page-a")
	mustAcquire(t, b, "page-a")
	b.release(old)
	time.Sleep(60 * time.Millisecond)
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true, false, true}) {
		t.Fatalf("grabs %v, want [true false true] and the panel still held", got)
	}
}

// Every page hears whether another page holds the panel (so it does not say
// "reacts again" while one does), but only a page that blocked it, or was
// the last to let go, can end the block early.
func TestTouchBlockDropByOtherPage(t *testing.T) {
	shortTimes(t, time.Minute, time.Hour)
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	held := mustAcquire(t, b, "page-a")
	if !b.drop("page-x") || !b.drop("") {
		t.Fatal("another page was told no one holds the panel")
	}
	if len(g.seen()) != 1 {
		t.Fatalf("another page's turning off changed the block: %v", g.seen())
	}
	b.release(held) // the grace starts, with page-a as the last holder
	if !b.drop("page-x") || len(g.seen()) != 1 {
		t.Fatalf("during page-a's grace another page was told no one holds the panel, or cut the grace short: %v", g.seen())
	}
	if b.drop("page-a") || !reflect.DeepEqual(g.seen(), []bool{true, false}) {
		t.Fatalf("the last holder could not end the grace: %v", g.seen())
	}
}

// A give-back whose timer fired just as a new hold stopped it (it then waits
// for the lock) finds its number outdated and leaves the panel taken.
func TestTouchBlockStaleGiveBack(t *testing.T) {
	shortTimes(t, time.Minute, time.Hour)
	g := &fakeGrab{}
	b := &touchBlock{grab: g.grab}
	b.release(mustAcquire(t, b, "page-a"))
	b.mu.Lock()
	stale := b.letGoGen // the first timer's number
	b.mu.Unlock()
	b.release(mustAcquire(t, b, "page-a")) // stopped it, then a new grace
	b.giveBack(stale)
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("a stopped timer gave the panel back: %v", got)
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

// ?unblock=1 releases the page's own block at once and says whether
// another page still blocks the panel.
func TestGestureHandlerUnblock(t *testing.T) {
	shortTimes(t, time.Minute, time.Hour)
	g := &fakeGrab{}
	h := NewGestureHandler(pubsub.NewPubSub(), moveGeometry, g.grab, nil)
	serve := func(url string) (*streamWriter, func()) {
		w := &streamWriter{header: http.Header{}}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
			close(done)
		}()
		return w, func() { cancel(); <-done }
	}
	wa, stopA := serve("/gestures?client=page-a&block=1")
	defer stopA()
	waitFor(t, wa, `{"touchBlocked":true}`)
	wb, stopB := serve("/gestures?client=page-b&block=1")
	defer stopB()
	waitFor(t, wb, `{"touchBlocked":true}`)

	wu, stopU := serve("/gestures?client=page-a&unblock=1")
	waitFor(t, wu, `{"touchBlocked":true,"elsewhere":true}`)
	stopU()

	stopB()
	wu, stopU = serve("/gestures?client=page-b&unblock=1")
	waitFor(t, wu, `{"touchBlocked":false,"released":true}`)
	stopU()
	if got := g.seen(); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("grabs %v, want [true false] without waiting for the grace", got)
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
