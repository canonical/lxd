package fsmonitor

// FSMonitor represents a filesystem monitor.
type FSMonitor interface {
	DriverName() string
	PrefixPath() string
	Watch(path string, identifier string, f func(path string, event Event) bool) error
	Unwatch(path string, identifier string) error
}
