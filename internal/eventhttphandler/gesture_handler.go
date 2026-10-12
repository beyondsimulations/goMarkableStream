package eventhttphandler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/pubsub"
)

// SwipeDirection ...
type SwipeDirection string

const (
	// SwipeLeft ...
	SwipeLeft SwipeDirection = "Swipe Left"
	// SwipeRight ...
	SwipeRight SwipeDirection = "Swipe Right"
)

// NewGestureHandler creates an event habdler that subscribes from the inputEvents
func NewGestureHandler(inputEvents *pubsub.PubSub) *GestureHandler {
	return &GestureHandler{
		inputEventBus: inputEvents,
	}
}

// GestureHandler is a http.Handler that detect touch gestures
type GestureHandler struct {
	inputEventBus *pubsub.PubSub
}

type gesture struct {
	leftDistance, rightDistance, upDistance, downDistance int64
}

func (g *gesture) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{ "left": %v, "right": %v, "up": %v, "down": %v}`+"\n", g.leftDistance, g.rightDistance, g.upDistance, g.downDistance)), nil
}

func (g *gesture) String() string {
	return fmt.Sprintf("Left: %v, Right: %v, Up: %v, Down: %v", g.leftDistance, g.rightDistance, g.upDistance, g.downDistance)
}

func (g *gesture) sum() int64 {
	return g.leftDistance + g.rightDistance + g.upDistance + g.downDistance
}

func (g *gesture) reset() {
	g.leftDistance = 0
	g.rightDistance = 0
	g.upDistance = 0
	g.downDistance = 0
}

const (
	codeXAxis uint16 = 54
	codeYAxis uint16 = 53
	// A gesture is a run of touch events with less than this between them.
	gestureMaxInterval = 150 * time.Millisecond
)

// swipeTracker adds up the distance a touch travels in each direction, from
// the touch panel's absolute position events.
type swipeTracker struct {
	gesture
	lastX, lastY int32
}

// observe adds one EV_ABS event. The first position on each axis only sets
// the starting point.
func (t *swipeTracker) observe(event events.InputEventFromSource) {
	switch event.Code {
	case codeXAxis:
		if t.lastX != 0 {
			distance := event.Value - t.lastX
			if distance < 0 {
				t.rightDistance += -int64(distance)
			} else {
				t.leftDistance += int64(distance)
			}
		}
		t.lastX = event.Value
	case codeYAxis:
		if t.lastY != 0 {
			distance := event.Value - t.lastY
			if distance < 0 {
				t.upDistance += -int64(distance)
			} else {
				t.downDistance += int64(distance)
			}
		}
		t.lastY = event.Value
	}
}

func (t *swipeTracker) reset() {
	t.gesture.reset()
	t.lastX = 0
	t.lastY = 0
}

// ServeHTTP implements http.Handler
func (h *GestureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Subscribe only to Touch events of type EvAbs
	touchSource := events.Touch
	absType := uint16(events.EvAbs)
	eventC := h.inputEventBus.SubscribeWithFilter("eventListener", pubsub.EventFilter{
		Source: &touchSource,
		Type:   &absType,
	})
	defer func() {
		h.inputEventBus.Unsubscribe(eventC)
	}()
	tick := time.NewTicker(gestureMaxInterval)
	defer tick.Stop()
	tracker := &swipeTracker{}

	enc := json.NewEncoder(w)
	w.Header().Set("Content-Type", "application/x-ndjson")

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			// TODO send last event
			if tracker.sum() != 0 {
				err := enc.Encode(&tracker.gesture)
				if err != nil {
					http.Error(w, "cannot send json encode the message "+err.Error(), http.StatusInternalServerError)
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			tracker.reset()
		case event := <-eventC:
			tracker.observe(event)
			tick.Reset(gestureMaxInterval)
		}
	}
}
