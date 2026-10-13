package eventhttphandler

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/events"
)

func touchAbs(code uint16, value int32) events.InputEventFromSource {
	return events.InputEventFromSource{
		Source:     events.Touch,
		InputEvent: events.InputEvent{Type: events.EvAbs, Code: code, Value: value},
	}
}

// swipe returns the events of one finger moving in steps of (dx, dy) on the
// two position axes, starting at (x, y). Like the kernel, it sends no
// ABS_MT_SLOT for a single finger.
func swipe(x, y, dx, dy int32, steps int) []events.InputEventFromSource {
	evs := []events.InputEventFromSource{touchAbs(codeTrackingID, 7)}
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
	if tr.sum() != 0 || tr.hasX || tr.hasY {
		t.Fatalf("reset left %v, hasX %v, hasY %v", tr.String(), tr.hasX, tr.hasY)
	}
}

// A swipe that starts on the panel's edge, at 0, counts its first step.
func TestSwipeTrackerStartsAtZero(t *testing.T) {
	var tr swipeTracker
	for _, ev := range swipe(0, 300, 50, 0, 10) {
		tr.observe(ev)
	}
	if tr.leftDistance != 500 {
		t.Fatalf("left = %d, want 500", tr.leftDistance)
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
		{gesture{rightDistance: 400}, "none", "nothing"},
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

// The log's rules must stay the same as the browser's checkSwipeDirection.
func TestSwipeRulesMatchClient(t *testing.T) {
	src, err := os.ReadFile("../../client/worker_gesture_processing.js")
	if err != nil {
		t.Fatal(err)
	}
	branch := regexp.MustCompile(`if \(([^)]*)\) \{\s*return '([a-z-]+)'`)
	cond := regexp.MustCompile(`json\.(left|right|up|down) ([<>]) (\d+)`)
	var fromJS []string
	for _, m := range branch.FindAllStringSubmatch(string(src), -1) {
		var parts []string
		for _, c := range cond.FindAllStringSubmatch(m[1], -1) {
			parts = append(parts, c[1]+c[2]+c[3])
		}
		slices.Sort(parts)
		fromJS = append(fromJS, m[2]+": "+strings.Join(parts, " "))
	}
	var fromGo []string
	for _, r := range swipeRules {
		var parts []string
		add := func(dir string, lo, hi int64) {
			if lo != 0 {
				parts = append(parts, dir+">"+strconv.FormatInt(lo, 10))
			}
			if hi != 0 {
				parts = append(parts, dir+"<"+strconv.FormatInt(hi, 10))
			}
		}
		add("left", r.minLeft, r.maxLeft)
		add("right", r.minRight, r.maxRight)
		add("up", r.minUp, r.maxUp)
		add("down", r.minDown, r.maxDown)
		slices.Sort(parts)
		fromGo = append(fromGo, r.name+": "+strings.Join(parts, " "))
	}
	if len(fromJS) == 0 || fmt.Sprint(fromJS) != fmt.Sprint(fromGo) {
		t.Fatalf("rules differ from the client:\n js: %v\n go: %v", fromJS, fromGo)
	}
}

func TestGestureLogLine(t *testing.T) {
	var g gestureLog
	start := time.Unix(0, 0)
	for i, ev := range swipe(1000, 500, -50, 2, 10) {
		g.observe(ev, 0, start.Add(time.Duration(i)*time.Millisecond))
	}
	line := g.String()
	for _, want := range []string{
		"24 events in 23ms",
		"1 touch-down(s)",
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

func TestLogGesturesOneLinePerGesture(t *testing.T) {
	eventC := make(chan events.InputEventFromSource, 200)
	logged := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		logGestures(ctx, eventC, nil, func(v ...any) { logged <- fmt.Sprint(v...) })
		close(done)
	}()
	next := func() string {
		select {
		case line := <-logged:
			return line
		case <-time.After(5 * time.Second):
			t.Fatal("no gesture logged")
			return ""
		}
	}

	for _, ev := range swipe(1000, 500, 60, 0, 10) {
		eventC <- ev
	}
	if line := next(); !strings.Contains(line, "browser: left (previous slide)") || !strings.Contains(line, "slots [0]") {
		t.Errorf("first gesture: %s", line)
	}

	// A second finger in slot 1; the slot stays 1 until it changes.
	eventC <- touchAbs(codeSlot, 1)
	for _, ev := range swipe(1000, 500, -60, 0, 10) {
		eventC <- ev
	}
	if line := next(); !strings.Contains(line, "browser: right (next slide)") || !strings.Contains(line, "slots [1]") {
		t.Errorf("second gesture: %s", line)
	}

	// A gesture still open when the server stops is logged too.
	for _, ev := range swipe(1000, 500, 0, 60, 10) {
		eventC <- ev
	}
	for len(eventC) > 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if line := next(); !strings.Contains(line, "slots [1]") || !strings.Contains(line, "browser: down") {
		t.Errorf("gesture at shutdown: %s", line)
	}
}
