//go:build !(linux && arm64)

package remarkable

// LoadTouchGeometry returns nil on the reMarkable 2 (and off the tablet):
// its gestures keep the original direction totals.
func LoadTouchGeometry() (*TouchGeometry, error) {
	return nil, nil
}
