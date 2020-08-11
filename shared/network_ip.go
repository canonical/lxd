package shared

import (
	"net"
)

// IPRange defines a range of IP addresses.
// Optionally just set Start to indicate a single IP.
type IPRange struct {
	Start net.IP
	End   net.IP
}
