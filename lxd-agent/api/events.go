package api

// DeviceEventAction represents a device event action for instance devices.
type DeviceEventAction string

// All supported device events for instance devices.
const (
	DeviceAdded   DeviceEventAction = "added"
	DeviceRemoved DeviceEventAction = "removed"
	DeviceUpdated DeviceEventAction = "updated"
)
