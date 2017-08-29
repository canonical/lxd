package util

import (
	"fmt"
	"net"

	"github.com/lxc/lxd/shared"
)

// CanonicalNetworkAddress parses the given network address and returns a
// string of the form "host:port", possibly filling it with the default port if
// it's missing.
func CanonicalNetworkAddress(address string) string {
	_, _, err := net.SplitHostPort(address)
	if err != nil {
		ip := net.ParseIP(address)
		if ip != nil && ip.To4() == nil {
			address = fmt.Sprintf("[%s]:%s", address, shared.DefaultPort)
		} else {
			address = fmt.Sprintf("%s:%s", address, shared.DefaultPort)
		}
	}
	return address
}
