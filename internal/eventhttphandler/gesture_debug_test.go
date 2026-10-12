package eventhttphandler

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/pubsub"
)

func touchAbs(code uint16, value int32) events.InputEventFromSource {
	return events.InputEventFromSource{
		Source:     events.Touch,
		InputEvent: events.InputEvent{Type: events.EvAbs, Code: code, Value: value},
	}
}

// swipe returns the events of one finger moving in steps of (dx, dy) on the
// two position axes, starting at (x, y).
func swipe(x, y, dx, dy int32, steps int) []events.InputEventFromSource {
	evs := []events.InputEventFromSource{touchAbs(codeSlot, 0), touchAbs(codeTrackingID, 7)}
	for i := 0; i <= steps; i++ {
		evs = append(evs, touchAbs(codeXAxis, x+int32(i)*dx), touchAbs(codeYAxis, y+int32(i)*dy))
	}
	return append(evs, touchAbs(codeTrackingID, -1))
}

func TestSwipeTrackerTotals(t *testing.T) {
	var tr swipeTracker
	for _, ev := range swipe(1000, 500, -50, 2, 10) {
		tr.observe(ev)
	}
	if tr.rightDistance != 500 || tr.leftDistance != 0 || tr.downDistance != 20 || tr.upDistance != 0 {
		t.Fatalf("totals = %v", tr.String())
	}
	tr.reset()
	if tr.sum() != 0 || tr.lastX != 0 || tr.lastY != 0 {
		t.Fatalf("reset left %v, last %d/%d", tr.String(), tr.lastX, tr.lastY)
	}
}

func TestSwipeSeenAs(t *testing.T) {
	tests := []struct {
		g          gesture
		name, does string
	}{
		{gesture{leftDistance: 500}, "left", "previous slide"},
		{gesture{rightDistance: 500, upDistance: 99}, "right", "next slide"},
		{gesture{rightDistance: 500, upDistance: 100}, "none", "nothing"},
		{gesture{upDistance: 401}, "up", "nothing"},
		{gesture{leftDistance: 700, downDistance: 700}, "topleft-to-bottomright", "next slide"},
		{gesture{rightDistance: 700, upDistance: 700}, "bottomright-to-topleft", "drawing on top"},
		{gesture{rightDistance: 300}, "none", "nothing"},
	}
	for _, tt := range tests {
		name, does := swipeSeenAs(&tt.g)
		if name != tt.name || does != tt.does {
			t.Errorf("%v: got %s (%s), want %s (%s)", tt.g.String(), name, does, tt.name, tt.does)
		}
	}
}

func TestGestureLogLine(t *testing.T) {
	var g gestureLog
	start := time.Unix(0, 0)
	for i, ev := range swipe(1000, 500, -50, 2, 10) {
		g.observe(ev, start.Add(time.Duration(i)*time.Millisecond))
	}
	line := g.String()
	for _, want := range []string{
		"25 events in 24ms",
		"1 finger(s) down",
		"slots [0]",
		"code 54: 1000 -> 500 (range 500..1000)",
		"code 53: 500 -> 520 (range 500..520)",
		"sent left=0 right=500 up=0 down=20",
		"browser: right (next slide)",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log line lacks %q:\n%s", want, line)
		}
	}
}

func TestLogGesturesWritesOneLinePerGesture(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	bus := pubsub.NewPubSub()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		LogGestures(ctx, bus)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond) // let it subscribe

	for _, ev := range swipe(1000, 500, 60, 0, 10) {
		bus.Publish(ev)
	}
	time.Sleep(gestureMaxInterval + 100*time.Millisecond)
	for _, ev := range swipe(1000, 500, -60, 0, 10) {
		bus.Publish(ev)
	}
	time.Sleep(gestureMaxInterval + 100*time.Millisecond)
	cancel()
	<-done

	out := buf.String()
	if n := strings.Count(out, "gesture: "); n != 2 {
		t.Fatalf("want 2 gesture lines, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "browser: left (previous slide)") || !strings.Contains(out, "browser: right (next slide)") {
		t.Errorf("unexpected log:\n%s", out)
	}
}
