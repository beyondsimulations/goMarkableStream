//go:build !rmmove || !linux || !arm64

package remarkable

// DetectDevice returns the device model this binary was built for. Only the
// Paper Pro Move build checks the hardware it runs on.
func DetectDevice() (DeviceModel, string, error) {
	return Model, Model.String(), nil
}
