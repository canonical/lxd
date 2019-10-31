package firewall

// Firewall represents an LXD firewall.
type Firewall interface {
	// Network
	NetworkAppend(protocol string, comment string, table string, chain string, rule ...string) error
	NetworkPrepend(protocol string, comment string, table string, chain string, rule ...string) error
	NetworkClear(protocol string, comment string, table string) error

	// Container
	ContainerPrepend(protocol string, comment string, table string, chain string, rule ...string) error
	ContainerClear(protocol string, comment string, table string, chain string, rule ...string) error
}
