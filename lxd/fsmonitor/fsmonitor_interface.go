package fsmonitor

const (
	// DriverNameFANotify is the name of the FANotify driver.
	//
	// FANotify should be preferred over INotify because it is more performant and does not need to recursively watch
	// subdirectories. However, it is not possible to use fanotify if the specified path is not a mountpoint because we
	// need to use the unix.FAN_MARK_FILESYSTEM flag for this functionality.
	DriverNameFANotify = "fanotify"

	// DriverNameINotify is the name of the inotify driver.
	DriverNameINotify = "inotify"
)

// FSMonitor represents a filesystem monitor.
type FSMonitor interface {
	DriverName() string
	PrefixPath() string
	Watch(path string, identifier string, f func(path string, event Event) bool) error
	Unwatch(path string, identifier string) error
}
