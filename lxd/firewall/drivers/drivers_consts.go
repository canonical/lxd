package drivers

import "fmt"

// FilterIPv6All used to indicate to firewall package to filter all IPv6 traffic.
const FilterIPv6All = "::"

// FilterIPv4All used to indicate to firewall package to filter all IPv4 traffic.
const FilterIPv4All = "0.0.0.0"

// ErrNotSupported is returned when the firewall driver doesn't support a feature.
var ErrNotSupported error = fmt.Errorf("Not supported")

// Info indicates which features are supported by the driver.
type Info struct {
	ACLs bool // Supports ACLs.
}
