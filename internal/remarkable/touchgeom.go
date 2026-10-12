package remarkable

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

// newTouchGeometry orients the two multi-touch axes like the screen: ABS_MT_
// POSITION_X runs along the screen's x unless its range is the long side of a
// portrait screen (or the short side of a landscape one), when they swap. On
// the Paper Pro Move both run like the screen, x rightward and y downward
// (measured with RK_DEBUG_GESTURES, 2026-10-12).
func newTouchGeometry(xMin, xMax, yMin, yMax int32, width, height int) *TouchGeometry {
	g := &TouchGeometry{
		XCode: 53, YCode: 54,
		XMin: xMin, XMax: xMax, YMin: yMin, YMax: yMax,
		Width: width, Height: height,
	}
	if (xMax-xMin > yMax-yMin) != (width > height) {
		g.XCode, g.YCode = 54, 53
		g.XMin, g.XMax, g.YMin, g.YMax = yMin, yMax, xMin, xMax
	}
	return g
}
