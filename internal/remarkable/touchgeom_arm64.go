//go:build linux && arm64

package remarkable

import (
	"fmt"
	"os"
	"unsafe"
)

// LoadTouchGeometry reads the Paper Pro's or Paper Pro Move's touch panel
// ranges, so the gestures follow the screen in any orientation.
func LoadTouchGeometry() (*TouchGeometry, error) {
	f, err := os.Open(TouchInputDevice)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var x, y absInfo
	if err := ioctl(f.Fd(), eviocgabs(53), unsafe.Pointer(&x)); err != nil {
		return nil, fmt.Errorf("touch x range: %w", err)
	}
	if err := ioctl(f.Fd(), eviocgabs(54), unsafe.Pointer(&y)); err != nil {
		return nil, fmt.Errorf("touch y range: %w", err)
	}
	if x.Maximum <= x.Minimum || y.Maximum <= y.Minimum {
		return nil, fmt.Errorf("touch ranges %d..%d and %d..%d are empty", x.Minimum, x.Maximum, y.Minimum, y.Maximum)
	}
	return newTouchGeometry(x.Minimum, x.Maximum, y.Minimum, y.Maximum, Config.Width, Config.Height), nil
}
