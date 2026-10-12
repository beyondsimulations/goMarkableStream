package remarkable

import "fmt"

// TouchGeometry maps the touch panel onto the screen: which raw axis runs
// along the screen's x (rightward) and y (downward), and their ranges, so a
// swipe can be measured in screen pixels.
type TouchGeometry struct {
	XCode, YCode           uint16
	XMin, XMax, YMin, YMax int32
	Width, Height          int // the visible screen, in pixels
}

// Pixels converts a raw movement on the two axes into screen pixels.
func (g *TouchGeometry) Pixels(dx, dy int32) (float64, float64) {
	return float64(dx) * float64(g.Width) / float64(g.XMax-g.XMin),
		float64(dy) * float64(g.Height) / float64(g.YMax-g.YMin)
}

// newTouchGeometry takes ABS_MT_POSITION_X along the screen's x (rightward)
// and ABS_MT_POSITION_Y along its y (downward), as on the Paper Pro Move
// (measured with RK_DEBUG_GESTURES, 2026-10-12). A panel whose x range is
// the long side of a portrait screen (or the short side of a landscape one)
// lies across the screen: turned one way or the other, which its ranges do
// not tell, so it is refused rather than guessed.
func newTouchGeometry(xMin, xMax, yMin, yMax int32, width, height int) (*TouchGeometry, error) {
	if (xMax-xMin > yMax-yMin) != (width > height) {
		return nil, fmt.Errorf("touch panel %d..%d x %d..%d lies across the %dx%d screen", xMin, xMax, yMin, yMax, width, height)
	}
	return &TouchGeometry{
		XCode: 53, YCode: 54,
		XMin: xMin, XMax: xMax, YMin: yMin, YMax: yMax,
		Width: width, Height: height,
	}, nil
}
