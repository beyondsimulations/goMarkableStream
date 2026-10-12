//go:build linux

package remarkable

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// absInfo is the kernel's struct input_absinfo.
type absInfo struct {
	Value, Minimum, Maximum, Fuzz, Flat, Resolution int32
}

// ioctl request numbers from linux/input.h: _IOC(_IOC_READ, 'E', nr, size).
func eviocgname(size uintptr) uintptr { return 2<<30 | size<<16 | 'E'<<8 | 0x06 }
func eviocgabs(code uintptr) uintptr {
	return 2<<30 | unsafe.Sizeof(absInfo{})<<16 | 'E'<<8 | (0x40 + code)
}

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// DescribeTouchDevice reports the touch panel's name and the ranges of the
// axes the gestures use, for RK_DEBUG_GESTURES.
func DescribeTouchDevice() string {
	f, err := os.Open(TouchInputDevice)
	if err != nil {
		return fmt.Sprintf("touch device %s: %v", TouchInputDevice, err)
	}
	defer f.Close()

	name := make([]byte, 256)
	devName := "unknown"
	if err := ioctl(f.Fd(), eviocgname(uintptr(len(name))), unsafe.Pointer(&name[0])); err == nil {
		devName = strings.TrimRight(string(name), "\x00")
	}

	axes := []struct {
		code  uintptr
		label string
	}{
		{0, "ABS_X"},
		{1, "ABS_Y"},
		{47, "ABS_MT_SLOT"},
		{53, "ABS_MT_POSITION_X"},
		{54, "ABS_MT_POSITION_Y"},
	}
	var parts []string
	for _, a := range axes {
		var info absInfo
		if err := ioctl(f.Fd(), eviocgabs(a.code), unsafe.Pointer(&info)); err != nil {
			continue
		}
		if info.Minimum == 0 && info.Maximum == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("code %d %s %d..%d (resolution %d)", a.code, a.label, info.Minimum, info.Maximum, info.Resolution))
	}
	ranges := strings.Join(parts, ", ")
	if ranges == "" {
		ranges = "axis ranges not readable"
	}
	return fmt.Sprintf("touch device %s %q: %s; screen %dx%d", TouchInputDevice, devName, ranges, Config.Width, Config.Height)
}
