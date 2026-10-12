package eventhttphandler

import (
	"context"
	"fmt"
	"log"
	"math/bits"
	"strings"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/pubsub"
	"github.com/owulveryck/goMarkableStream/internal/remarkable"
)

// Multi-touch codes the log reports besides the two position axes.
const (
	codeSlot       uint16 = 47 // ABS_MT_SLOT: which finger the next events are for
	codeTrackingID uint16 = 57 // ABS_MT_TRACKING_ID: a new finger (>= 0) or a lifted one (-1)
)

// axisTrace follows one position axis through a gesture.
type axisTrace struct {
	seen                bool
	first, last, lo, hi int32
}

func (a *axisTrace) observe(v int32) {
	if !a.seen {
		a.seen = true
		a.first, a.lo, a.hi = v, v, v
	}
	a.last = v
	a.lo = min(a.lo, v)
	a.hi = max(a.hi, v)
}

func (a axisTrace) String() string {
	if !a.seen {
		return "no events"
	}
	return fmt.Sprintf("%d -> %d (range %d..%d)", a.first, a.last, a.lo, a.hi)
}

// gestureLog records one gesture for RK_DEBUG_GESTURES: the totals computed
// the same way as for the browser, plus where each axis started and ended
// and which fingers took part, so the gestures can be tuned per device.
type gestureLog struct {
	reader      *swipeReader // shared across gestures; nil on the reMarkable 2
	tracker     swipeTracker
	x, y        axisTrace // codeXAxis (54) and codeYAxis (53)
	events      int
	touchDowns  int
	slots       uint32 // bit n: slot n sent a position
	start, last time.Time
}

// observe adds one event; slot is the multi-touch slot it belongs to.
func (g *gestureLog) observe(event events.InputEventFromSource, slot int32, now time.Time) {
	if g.events == 0 {
		g.start = now
	}
	g.events++
	g.last = now
	if g.reader != nil {
		g.reader.observe(event)
	} else {
		g.tracker.observe(event)
	}
	switch event.Code {
	case codeXAxis, codeYAxis:
		if event.Code == codeXAxis {
			g.x.observe(event.Value)
		} else {
			g.y.observe(event.Value)
		}
		if slot >= 0 && slot < 32 {
			g.slots |= 1 << slot
		}
	case codeTrackingID:
		if event.Value >= 0 {
			g.touchDowns++
		}
	}
}

func (g *gestureLog) String() string {
	var slots []string
	for s := g.slots; s != 0; s &= s - 1 {
		slots = append(slots, fmt.Sprint(bits.TrailingZeros32(s)))
	}
	line := fmt.Sprintf("gesture: %d events in %v, %d touch-down(s), slots [%s] | code %d: %v | code %d: %v",
		g.events, g.last.Sub(g.start).Round(time.Millisecond), g.touchDowns, strings.Join(slots, " "),
		codeXAxis, g.x, codeYAxis, g.y)
	if g.reader != nil {
		// What a browser makes of it at each view rotation (the R key turns
		// the view by 90 degrees).
		dx, dy := g.reader.finger.pixels(g.reader.geometry)
		seen := make([]string, 0, 4)
		for _, rotation := range []int{0, 90, 180, 270} {
			name := g.reader.name(rotation)
			if name == "" {
				name = "none"
			}
			seen = append(seen, fmt.Sprintf("%d°: %s", rotation, name))
		}
		return fmt.Sprintf("%s | first finger dx=%+.0f dy=%+.0f px | browser %s", line, dx, dy, strings.Join(seen, ", "))
	}
	name, action := swipeSeenAs(&g.tracker.gesture)
	return fmt.Sprintf("%s | sent left=%d right=%d up=%d down=%d | browser: %s (%s)", line,
		g.tracker.leftDistance, g.tracker.rightDistance, g.tracker.upDistance, g.tracker.downDistance,
		name, action)
}

// swipeRule is one branch of checkSwipeDirection in
// client/worker_gesture_processing.js: every total must be above its
// minimum (when set) and below its maximum.
type swipeRule struct {
	name, action                      string
	minLeft, minRight, minUp, minDown int64
	maxLeft, maxRight, maxUp, maxDown int64
}

// swipeRules mirrors checkSwipeDirection (TestSwipeRulesMatchClient checks
// it) and the actions in client/workersHandling.js, for the log only.
var swipeRules = []swipeRule{
	{name: "left", action: "next slide", minLeft: 400, maxRight: 100, maxUp: 100, maxDown: 100},
	{name: "right", action: "previous slide", minRight: 400, maxLeft: 100, maxUp: 100, maxDown: 100},
	{name: "up", action: "nothing", minUp: 400, maxRight: 100, maxLeft: 100, maxDown: 100},
	{name: "down", action: "nothing", minDown: 400, maxRight: 100, maxUp: 100, maxLeft: 100},
	{name: "topright-to-bottomleft", action: "previous slide", minRight: 600, minDown: 600, maxUp: 50, maxLeft: 50},
	{name: "topleft-to-bottomright", action: "next slide", minLeft: 600, minDown: 600, maxUp: 50, maxRight: 50},
	{name: "bottomleft-to-topright", action: "content on top", minLeft: 600, minUp: 600, maxDown: 50, maxRight: 50},
	{name: "bottomright-to-topleft", action: "drawing on top", minRight: 600, minUp: 600, maxDown: 50, maxLeft: 50},
}

func (r swipeRule) matches(g *gesture) bool {
	within := func(v, lo, hi int64) bool {
		return (lo == 0 || v > lo) && (hi == 0 || v < hi)
	}
	return within(g.leftDistance, r.minLeft, r.maxLeft) &&
		within(g.rightDistance, r.minRight, r.maxRight) &&
		within(g.upDistance, r.minUp, r.maxUp) &&
		within(g.downDistance, r.minDown, r.maxDown)
}

// swipeSeenAs is what the browser makes of a gesture's totals.
func swipeSeenAs(g *gesture) (name, action string) {
	for _, r := range swipeRules {
		if r.matches(g) {
			return r.name, r.action
		}
	}
	return "none", "nothing"
}

// LogGestures writes one log line per touch gesture until ctx ends. It runs
// whether or not a browser is connected.
func LogGestures(ctx context.Context, inputEvents *pubsub.PubSub, geometry *remarkable.TouchGeometry) {
	touchSource := events.Touch
	absType := uint16(events.EvAbs)
	eventC := inputEvents.SubscribeWithFilter("gestureDebug", pubsub.EventFilter{
		Source: &touchSource,
		Type:   &absType,
	})
	defer inputEvents.Unsubscribe(eventC)
	logGestures(ctx, eventC, geometry, log.Print)
}

func logGestures(ctx context.Context, eventC <-chan events.InputEventFromSource, geometry *remarkable.TouchGeometry, logf func(...any)) {
	quiet := time.NewTimer(gestureMaxInterval)
	defer quiet.Stop()
	// The kernel only sends ABS_MT_SLOT when the slot changes, so the
	// current slot carries over from one gesture to the next.
	var slot int32
	var reader *swipeReader
	if geometry != nil {
		reader = &swipeReader{geometry: geometry}
	}
	current := &gestureLog{reader: reader}
	flush := func() {
		if current.events > 0 {
			logf(current.String())
			if reader != nil {
				reader.endGesture()
			}
			current = &gestureLog{reader: reader}
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-quiet.C:
			flush()
		case event := <-eventC:
			if event.Code == codeSlot {
				slot = event.Value
			}
			current.observe(event, slot, time.Now())
			quiet.Reset(gestureMaxInterval)
		}
	}
}
