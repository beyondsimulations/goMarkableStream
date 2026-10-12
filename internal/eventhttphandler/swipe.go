package eventhttphandler

import (
	"math"
	"math/bits"
	"strconv"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/remarkable"
)

// fingerTrack follows the first finger of a gesture from where it touched
// down to where it is now. Other fingers only count as touch-downs.
type fingerTrack struct {
	primary    int32
	hasPrimary bool
	x0, y0     int32
	x1, y1     int32
	hasX, hasY bool
	touchDowns int
	// The most fingers that were down at once during the gesture, counting
	// one that rests on the panel since an earlier gesture.
	maxContacts int
}

// observe adds one EV_ABS event; slot is the multi-touch slot it belongs to.
func (f *fingerTrack) observe(event events.InputEventFromSource, slot int32, g *remarkable.TouchGeometry) {
	if event.Code == codeTrackingID {
		if event.Value >= 0 {
			f.touchDowns++
		}
		return
	}
	if event.Code != g.XCode && event.Code != g.YCode {
		return
	}
	if !f.hasPrimary {
		f.primary, f.hasPrimary = slot, true
	}
	if slot != f.primary {
		return
	}
	if event.Code == g.XCode {
		if !f.hasX {
			f.x0, f.hasX = event.Value, true
		}
		f.x1 = event.Value
	} else {
		if !f.hasY {
			f.y0, f.hasY = event.Value, true
		}
		f.y1 = event.Value
	}
}

func (f *fingerTrack) reset() {
	*f = fingerTrack{}
}

// swipeReader reads one gesture at a time from the touch panel's events.
// The current slot and which slots have a finger down carry over between
// gestures: the kernel only reports them when they change.
type swipeReader struct {
	geometry *remarkable.TouchGeometry
	slot     int32
	live     uint32 // bit n: a finger is down in slot n
	finger   fingerTrack
}

func (r *swipeReader) observe(event events.InputEventFromSource) {
	switch event.Code {
	case codeSlot:
		r.slot = event.Value
	case codeTrackingID:
		if r.slot >= 0 && r.slot < 32 {
			if event.Value >= 0 {
				r.live |= 1 << r.slot
			} else {
				r.live &^= 1 << r.slot
			}
		}
	}
	r.finger.observe(event, r.slot, r.geometry)
	r.finger.maxContacts = max(r.finger.maxContacts, bits.OnesCount32(r.live))
}

// name is the gesture's swipe for a browser with the given view rotation.
func (r *swipeReader) name(rotation int) string {
	return swipeName(&r.finger, r.geometry, rotation)
}

// endGesture starts the next gesture; fingers still down stay counted.
func (r *swipeReader) endGesture() {
	r.finger.reset()
}

// pixels is the first finger's movement in screen pixels, x rightward and y
// downward as the tablet shows its page upright in portrait.
func (f *fingerTrack) pixels(g *remarkable.TouchGeometry) (float64, float64) {
	var dx, dy int32
	if f.hasX {
		dx = f.x1 - f.x0
	}
	if f.hasY {
		dy = f.y1 - f.y0
	}
	return g.Pixels(dx, dy)
}

// parseViewRotation reads the browser's view rotation: how far, clockwise,
// it turns the tablet's portrait page (0, 90, 180 or 270).
func parseViewRotation(s string) int {
	r, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	switch r {
	case 90, 180, 270:
		return r
	}
	return 0
}

// rotate turns a movement (x rightward, y downward) clockwise by the view
// rotation, so it reads as seen by someone holding the tablet that way.
func rotate(dx, dy float64, rotation int) (float64, float64) {
	switch rotation {
	case 90:
		return -dy, dx
	case 180:
		return -dx, -dy
	case 270:
		return dy, -dx
	}
	return dx, dy
}

// Swipe thresholds, as measured on the Paper Pro Move: a swipe travels at
// least 15% of the screen's short side; straight swipes stay within 20
// degrees of an edge, diagonals between 28 and 65 degrees (people draw them
// steep), and anything in between is ignored.
const (
	minSwipeShare  = 0.15
	maxStraightDeg = 20
	minDiagonalDeg = 28
	maxDiagonalDeg = 65
	minVerticalDeg = 70
)

// classifySwipe names a single-finger movement as the person sees it:
// swipe-left (from right to left), swipe-right, swipe-up, swipe-down, or a
// diagonal such as swipe-up-left. It returns "" for anything else.
func classifySwipe(dx, dy, shortSide float64) string {
	if math.Hypot(dx, dy) < minSwipeShare*shortSide {
		return ""
	}
	horizontal := "right"
	if dx < 0 {
		horizontal = "left"
	}
	vertical := "down"
	if dy < 0 {
		vertical = "up"
	}
	angle := math.Atan2(math.Abs(dy), math.Abs(dx)) * 180 / math.Pi
	switch {
	case angle <= maxStraightDeg:
		return "swipe-" + horizontal
	case angle >= minVerticalDeg:
		return "swipe-" + vertical
	case angle >= minDiagonalDeg && angle <= maxDiagonalDeg:
		return "swipe-" + vertical + "-" + horizontal
	}
	return ""
}

// swipeName is what one gesture means for a browser with the given view
// rotation; "" when it is no swipe or more than one finger touched.
func swipeName(f *fingerTrack, g *remarkable.TouchGeometry, rotation int) string {
	if f.touchDowns > 1 || f.maxContacts > 1 || !f.hasPrimary {
		return ""
	}
	dx, dy := f.pixels(g)
	dx, dy = rotate(dx, dy, rotation)
	return classifySwipe(dx, dy, float64(min(g.Width, g.Height)))
}
