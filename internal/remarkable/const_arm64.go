//go:build arm64 && !rmmove

package remarkable

const (
	Model = RemarkablePaperPro

	// ScreenWidth of the remarkable paper pro
	ScreenWidth = 1632
	// ScreenHeight of the remarkable paper pro
	ScreenHeight = 2154

	ScreenSizeBytes = ScreenWidth * ScreenHeight * 4

	// FramebufferStorageWidth is the width of one framebuffer row in memory,
	// in pixels. Rows are not padded on the Paper Pro.
	FramebufferStorageWidth = ScreenWidth
	// FramebufferStorageSizeBytes is the size of the buffer as stored in memory.
	FramebufferStorageSizeBytes = ScreenSizeBytes

	// These values are from Max values of /dev/input/event2 (ABS_X and ABS_Y)
	MaxXValue = 11180
	MaxYValue = 15340

	PenInputDevice   = "/dev/input/event2"
	TouchInputDevice = "/dev/input/event3"
)
