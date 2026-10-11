package remarkable

// Screen profile of the reMarkable Paper Pro Move ("Chiappa"). These values
// carry no build tag so they can be unit-tested on any architecture; they are
// only used as device constants in builds with the rmmove tag.
const (
	moveScreenWidth      = 954
	moveScreenHeight     = 1696
	moveStorageWidth     = 960
	moveMaxXValue        = 6760
	moveMaxYValue        = 11960
	moveVisibleSizeBytes = moveScreenWidth * moveScreenHeight * BytesPerPixelBGRA
	moveStorageSizeBytes = moveStorageWidth * moveScreenHeight * BytesPerPixelBGRA
)
