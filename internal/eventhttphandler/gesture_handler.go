package eventhttphandler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/pubsub"
	"github.com/owulveryck/goMarkableStream/internal/remarkable"
)

// SwipeDirection ...
type SwipeDirection string

const (
	// SwipeLeft ...
	SwipeLeft SwipeDirection = "Swipe Left"
	// SwipeRight ...
	SwipeRight SwipeDirection = "Swipe Right"
)

// NewGestureHandler creates an event habdler that subscribes from the inputEvents.
// With a touch geometry (Paper Pro, Paper Pro Move) it names each swipe as
// the browser's view shows the tablet; without one (reMarkable 2) it sends
// the direction totals and the browser names them. grabTouch takes the touch
// panel from xochitl or gives it back (nil where it cannot); allowBlock, if
// set, says why taking it is refused right now.
func NewGestureHandler(inputEvents *pubsub.PubSub, geometry *remarkable.TouchGeometry, grabTouch func(bool) error, allowBlock func() error) *GestureHandler {
	return &GestureHandler{
		inputEventBus: inputEvents,
		geometry:      geometry,
		block:         &touchBlock{grab: grabTouch, allow: allowBlock},
	}
}

// GestureHandler is a http.Handler that detect touch gestures
type GestureHandler struct {
	inputEventBus *pubsub.PubSub
	geometry      *remarkable.TouchGeometry
	block         *touchBlock
}

// swipeMessage is one named swipe, for example {"swipe": "swipe-left"}.
type swipeMessage struct {
	Swipe string `json:"swipe"`
}

// blockMessage answers ?block=1 first: whether xochitl now ignores the
// fingers, and why not.
type blockMessage struct {
	TouchBlocked bool   `json:"touchBlocked"`
	Error        string `json:"error,omitempty"`
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
	hasX, hasY   bool // 0 is a real position at the panel's edge
}

// observe adds one EV_ABS event. The first position on each axis only sets
// the starting point.
func (t *swipeTracker) observe(event events.InputEventFromSource) {
	switch event.Code {
	case codeXAxis:
		if t.hasX {
			distance := event.Value - t.lastX
			if distance < 0 {
				t.rightDistance += -int64(distance)
			} else {
				t.leftDistance += int64(distance)
			}
		}
		t.lastX, t.hasX = event.Value, true
	case codeYAxis:
		if t.hasY {
			distance := event.Value - t.lastY
			if distance < 0 {
				t.upDistance += -int64(distance)
			} else {
				t.downDistance += int64(distance)
			}
		}
		t.lastY, t.hasY = event.Value, true
	}
}

func (t *swipeTracker) reset() {
	*t = swipeTracker{}
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
	// ?rotation= is how far the browser turns the tablet's portrait page.
	rotation := parseViewRotation(r.URL.Query().Get("rotation"))
	tick := time.NewTicker(gestureMaxInterval)
	defer tick.Stop()
	tracker := &swipeTracker{}
	reader := &swipeReader{geometry: h.geometry}

	enc := json.NewEncoder(w)
	w.Header().Set("Content-Type", "application/x-ndjson")

	// ?block=1 keeps xochitl from seeing the fingers while this request
	// lasts; the swipes still reach this stream. The request ends after
	// blockLease, between gestures, and the browser asks again at once, so
	// a page that went away without closing its connection lets go.
	var lease <-chan time.Time
	if r.URL.Query().Get("block") == "1" {
		status := blockMessage{TouchBlocked: true}
		if err := h.block.acquire(); err != nil {
			log.Printf("Touch: cannot block the touch panel: %v", err)
			status = blockMessage{Error: err.Error()}
		} else {
			defer h.block.release()
			leaseTimer := time.NewTimer(blockLease)
			defer leaseTimer.Stop()
			lease = leaseTimer.C
		}
		if err := enc.Encode(status); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	// pending: events since the last tick, so a gesture may be under way.
	var pending, leaseOver bool
	for {
		select {
		case <-r.Context().Done():
			return
		case <-lease:
			if !pending {
				return
			}
			leaseOver = true // after this gesture
		case <-tick.C:
			var message any
			if h.geometry != nil {
				if name := reader.name(rotation); name != "" {
					message = swipeMessage{Swipe: name}
				}
			} else if tracker.sum() != 0 {
				message = &tracker.gesture
			}
			if message != nil {
				if err := enc.Encode(message); err != nil {
					http.Error(w, "cannot send json encode the message "+err.Error(), http.StatusInternalServerError)
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			tracker.reset()
			reader.endGesture()
			pending = false
			if leaseOver {
				return
			}
		case event := <-eventC:
			pending = true
			if h.geometry != nil {
				reader.observe(event)
			} else {
				tracker.observe(event)
			}
			tick.Reset(gestureMaxInterval)
		}
	}
}
