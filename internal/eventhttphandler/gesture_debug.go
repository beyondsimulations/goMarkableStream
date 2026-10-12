package eventhttphandler

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/pubsub"
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

// gestureLog records one gesture for RK_DEBUG_GESTURES: the totals the
// browser receives, plus where each axis started and ended and how many
// fingers were down, so the gestures can be tuned per device.
type gestureLog struct {
	tracker     swipeTracker
	x, y        axisTrace // codeXAxis (54) and codeYAxis (53)
	events      int
	fingers     int
	slots       map[int32]bool
	start, last time.Time
}

func (g *gestureLog) observe(event events.InputEventFromSource, now time.Time) {
	if g.events == 0 {
		g.start = now
		g.slots = map[int32]bool{}
	}
	g.events++
	g.last = now
	g.tracker.observe(event)
	switch event.Code {
	case codeXAxis:
		g.x.observe(event.Value)
	case codeYAxis:
		g.y.observe(event.Value)
	case codeSlot:
		g.slots[event.Value] = true
	case codeTrackingID:
		if event.Value >= 0 {
			g.fingers++
		}
	}
}

func (g *gestureLog) String() string {
	ids := make([]int32, 0, len(g.slots))
	for s := range g.slots {
		ids = append(ids, s)
	}
	slices.Sort(ids)
	slots := make([]string, len(ids))
	for i, s := range ids {
		slots[i] = fmt.Sprint(s)
	}
	name, action := swipeSeenAs(&g.tracker.gesture)
	return fmt.Sprintf("gesture: %d events in %v, %d finger(s) down, slots [%s] | code %d: %v | code %d: %v | sent left=%d right=%d up=%d down=%d | browser: %s (%s)",
		g.events, g.last.Sub(g.start).Round(time.Millisecond), g.fingers, strings.Join(slots, " "),
		codeXAxis, g.x, codeYAxis, g.y,
		g.tracker.leftDistance, g.tracker.rightDistance, g.tracker.upDistance, g.tracker.downDistance,
		name, action)
}

// swipeSeenAs mirrors checkSwipeDirection in client/worker_gesture_processing.js
// and the actions in client/workersHandling.js, for the log only.
func swipeSeenAs(g *gesture) (name, action string) {
	l, r, u, d := g.leftDistance, g.rightDistance, g.upDistance, g.downDistance
	switch {
	case l > 400 && r < 100 && u < 100 && d < 100:
		return "left", "previous slide"
	case r > 400 && l < 100 && u < 100 && d < 100:
		return "right", "next slide"
	case u > 400 && r < 100 && l < 100 && d < 100:
		return "up", "nothing"
	case d > 400 && r < 100 && u < 100 && l < 100:
		return "down", "nothing"
	case r > 600 && d > 600 && u < 50 && l < 50:
		return "topright-to-bottomleft", "previous slide"
	case l > 600 && d > 600 && u < 50 && r < 50:
		return "topleft-to-bottomright", "next slide"
	case l > 600 && u > 600 && d < 50 && r < 50:
		return "bottomleft-to-topright", "content on top"
	case r > 600 && u > 600 && d < 50 && l < 50:
		return "bottomright-to-topleft", "drawing on top"
	default:
		return "none", "nothing"
	}
}

// LogGestures writes one log line per touch gesture until ctx ends. It runs
// whether or not a browser is connected.
func LogGestures(ctx context.Context, inputEvents *pubsub.PubSub) {
	touchSource := events.Touch
	absType := uint16(events.EvAbs)
	eventC := inputEvents.SubscribeWithFilter("gestureDebug", pubsub.EventFilter{
		Source: &touchSource,
		Type:   &absType,
	})
	defer inputEvents.Unsubscribe(eventC)

	quiet := time.NewTimer(gestureMaxInterval)
	defer quiet.Stop()
	current := &gestureLog{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-quiet.C:
			if current.events > 0 {
				log.Print(current)
				current = &gestureLog{}
			}
		case event := <-eventC:
			current.observe(event, time.Now())
			quiet.Reset(gestureMaxInterval)
		}
	}
}
