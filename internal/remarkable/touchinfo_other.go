//go:build !linux

package remarkable

// DescribeTouchDevice reports the touch panel on the tablet only.
func DescribeTouchDevice() string {
	return "touch device: only readable on the tablet (Linux)"
}
