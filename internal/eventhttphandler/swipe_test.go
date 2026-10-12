package eventhttphandler

import (
	"testing"

	"github.com/owulveryck/goMarkableStream/internal/events"
	"github.com/owulveryck/goMarkableStream/internal/remarkable"
)

// moveGeometry is the Paper Pro Move's panel as RK_DEBUG_GESTURES reported
// it: x (53) 0..1248, y (54) 0..2208, screen 954x1696.
var moveGeometry = &remarkable.TouchGeometry{XCode: 53, YCode: 54, XMax: 1248, YMax: 2208, Width: 954, Height: 1696}

// track replays one finger from (x0, y0) to (x1, y1).
func track(x0, y0, x1, y1 int32) *fingerTrack {
	var f fingerTrack
	for _, ev := range []events.InputEventFromSource{
		touchAbs(codeTrackingID, 7),
		touchAbs(53, x0), touchAbs(54, y0),
		touchAbs(53, x1), touchAbs(54, y1),
		touchAbs(codeTrackingID, -1),
	} {
		f.observe(ev, 0, moveGeometry)
	}
	return &f
}

// The start and end of each swipe Tobias drew on the Paper Pro Move on
// 2026-10-12, first holding it upright (view rotation 0), then in landscape
// with its top edge on the right (the browser's rotated view, 90).
func TestSwipeNameOnMove(t *testing.T) {
	tests := []struct {
		drawn          string
		rotation       int
		x0, y0, x1, y1 int32
		want           string
	}{
		{"tap", 0, 692, 208, 700, 210, ""},
		{"right to left", 0, 1057, 1216, 56, 1168, "swipe-left"},
		{"right to left", 0, 1166, 1331, 104, 1364, "swipe-left"},
		{"left to right", 0, 49, 1233, 1223, 1239, "swipe-right"},
		{"left to right", 0, 109, 1179, 1196, 1181, "swipe-right"},
		{"top to bottom", 0, 617, 764, 603, 1848, "swipe-down"},
		{"bottom to top", 0, 618, 1671, 691, 976, "swipe-up"},
		{"top-left to bottom-right", 0, 0, 25, 663, 1078, "swipe-down-right"},
		{"bottom-right to top-left", 0, 1195, 2208, 426, 900, "swipe-up-left"},

		{"right to left", 90, 578, 264, 715, 1529, "swipe-left"},
		{"right to left", 90, 551, 265, 629, 1487, "swipe-left"},
		{"left to right", 90, 618, 1806, 641, 324, "swipe-right"},
		{"left to right", 90, 726, 1793, 709, 543, "swipe-right"},
		{"top to bottom", 90, 302, 1180, 1248, 1193, "swipe-down"},
		{"bottom to top", 90, 1239, 1124, 530, 1152, "swipe-up"},
		{"top-left to bottom-right", 90, 0, 2208, 798, 1130, "swipe-down-right"},
		{"bottom-right to top-left", 90, 1248, 7, 496, 1165, "swipe-up-left"},
	}
	for _, tt := range tests {
		got := swipeName(track(tt.x0, tt.y0, tt.x1, tt.y1), moveGeometry, tt.rotation)
		if got != tt.want {
			t.Errorf("%s at %d°: got %q, want %q", tt.drawn, tt.rotation, got, tt.want)
		}
	}
}

// Turning the view turns the same movement: a finger moving toward the
// page's bottom edge is a right-to-left swipe when the top edge is on the
// right (90) and a left-to-right one when it is on the left (270).
func TestSwipeNameFollowsViewRotation(t *testing.T) {
	f := track(600, 300, 600, 1500)
	for rotation, want := range map[int]string{0: "swipe-down", 90: "swipe-left", 180: "swipe-up", 270: "swipe-right"} {
		if got := swipeName(f, moveGeometry, rotation); got != want {
			t.Errorf("rotation %d: got %q, want %q", rotation, got, want)
		}
	}
}

func TestSwipeNameIgnoresSecondFinger(t *testing.T) {
	var f fingerTrack
	for _, step := range []struct {
		ev   events.InputEventFromSource
		slot int32
	}{
		{touchAbs(codeTrackingID, 1), 0},
		{touchAbs(53, 1100), 0}, {touchAbs(54, 1200), 0},
		{touchAbs(codeTrackingID, 2), 1},
		{touchAbs(53, 100), 1}, {touchAbs(54, 1200), 1},
		{touchAbs(53, 100), 0},
	} {
		f.observe(step.ev, step.slot, moveGeometry)
	}
	if got := swipeName(&f, moveGeometry, 0); got != "" {
		t.Fatalf("two fingers: got %q", got)
	}
	// The first finger alone moved from 1100 to 100: the second finger's
	// positions are not part of its track.
	if f.x0 != 1100 || f.x1 != 100 {
		t.Fatalf("first finger x %d -> %d, want 1100 -> 100", f.x0, f.x1)
	}
}

func TestClassifySwipeAngles(t *testing.T) {
	tests := []struct {
		dx, dy float64
		want   string
	}{
		{-500, 0, "swipe-left"},
		{500, 180, "swipe-right"}, // 19.8 degrees
		{500, 200, ""},            // 21.8: between straight and diagonal
		{500, 500, "swipe-down-right"},
		{-300, -600, "swipe-up-left"}, // 63.4
		{100, 400, "swipe-down"},      // 76
		{0, -400, "swipe-up"},
		{100, 0, ""}, // shorter than 15% of 954
	}
	for _, tt := range tests {
		if got := classifySwipe(tt.dx, tt.dy, 954); got != tt.want {
			t.Errorf("(%v, %v): got %q, want %q", tt.dx, tt.dy, got, tt.want)
		}
	}
}

func TestParseViewRotation(t *testing.T) {
	for in, want := range map[string]int{"": 0, "90": 90, "180": 180, "270": 270, "45": 0, "x": 0} {
		if got := parseViewRotation(in); got != want {
			t.Errorf("%q: got %d, want %d", in, got, want)
		}
	}
}
