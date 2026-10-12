package remarkable

import "testing"

func TestNewTouchGeometryMove(t *testing.T) {
	// The Paper Pro Move: x 0..1248, y 0..2208 on a 954x1696 screen.
	g, err := newTouchGeometry(0, 1248, 0, 2208, 954, 1696)
	if err != nil {
		t.Fatal(err)
	}
	if g.XCode != 53 || g.YCode != 54 || g.XMax != 1248 || g.YMax != 2208 {
		t.Fatalf("geometry = %+v", g)
	}
	dx, dy := g.Pixels(1248, 2208)
	if dx != 954 || dy != 1696 {
		t.Fatalf("full travel = %v x %v px, want 954 x 1696", dx, dy)
	}
}

// A panel lying across the screen is refused: its ranges do not tell which
// way it is turned, and swapping the axes alone would mirror the swipes.
func TestNewTouchGeometryRefusesPanelAcrossScreen(t *testing.T) {
	if g, err := newTouchGeometry(0, 2208, 0, 1248, 954, 1696); err == nil {
		t.Fatalf("got %+v, want an error", g)
	}
}
