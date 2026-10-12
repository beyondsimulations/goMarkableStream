//go:build linux

package remarkable

import "testing"

// The request numbers must match linux/input.h on every architecture.
func TestEvdevIoctlNumbers(t *testing.T) {
	if got := eviocgname(256); got != 0x81004506 {
		t.Errorf("EVIOCGNAME(256) = %#x, want 0x81004506", got)
	}
	if got := eviocgabs(53); got != 0x80184575 {
		t.Errorf("EVIOCGABS(ABS_MT_POSITION_X) = %#x, want 0x80184575", got)
	}
}
