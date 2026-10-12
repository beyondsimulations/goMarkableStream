package remarkable

import "testing"

func TestNewTouchGeometryMove(t *testing.T) {
	// The Paper Pro Move: x 0..1248, y 0..2208 on a 954x1696 screen.
	g := newTouchGeometry(0, 1248, 0, 2208, 954, 1696)
	if g.XCode != 53 || g.YCode != 54 || g.XMax != 1248 || g.YMax != 2208 {
		t.Fatalf("geometry = %+v", g)
	}
	dx, dy := g.Pixels(1248, 2208)
	if dx != 954 || dy != 1696 {
		t.Fatalf("full travel = %v x %v px, want 954 x 1696", dx, dy)
	}
}

// A panel whose x axis is the long side of a portrait screen is turned.
func TestNewTouchGeometrySwapsLandscapePanel(t *testing.T) {
	g := newTouchGeometry(0, 2208, 0, 1248, 954, 1696)
	if g.XCode != 54 || g.YCode != 53 || g.XMax != 1248 || g.YMax != 2208 {
		t.Fatalf("geometry = %+v", g)
	}
}
