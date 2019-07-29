package device

import (
	"fmt"
	"net"
	"strings"

	"github.com/lxc/lxd/shared"
)

// ProxyAddress represents a proxy address configuration.
type ProxyAddress struct {
	ConnType string
	Addr     []string
	Abstract bool
}

// ProxyParseAddr validates a proxy address and parses it into its constituent parts.
func ProxyParseAddr(addr string) (*ProxyAddress, error) {
	// Split into <protocol> and <address>.
	fields := strings.SplitN(addr, ":", 2)

	if !shared.StringInSlice(fields[0], []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Unknown connection type '%s'", fields[0])
	}

	newProxyAddr := &ProxyAddress{
		ConnType: fields[0],
		Abstract: strings.HasPrefix(fields[1], "@"),
	}

	// unix addresses cannot have ports.
	if newProxyAddr.ConnType == "unix" {
		newProxyAddr.Addr = []string{fields[1]}
		return newProxyAddr, nil
	}

	// Split <address> into <address> and <ports>.
	address, port, err := net.SplitHostPort(fields[1])
	if err != nil {
		return nil, err
	}

	// Validate that it's a valid address.
	if shared.StringInSlice(newProxyAddr.ConnType, []string{"udp", "tcp"}) {
		err := NetworkValidAddress(address)
		if err != nil {
			return nil, err
		}
	}

	// Split <ports> into individual ports and port ranges.
	ports := strings.SplitN(port, ",", -1)
	for _, p := range ports {
		portFirst, portRange, err := networkParsePortRange(p)
		if err != nil {
			return nil, err
		}

		for i := int64(0); i < portRange; i++ {
			var newAddr string
			if strings.Contains(address, ":") {
				// IPv6 addresses need to be enclosed in square brackets.
				newAddr = fmt.Sprintf("[%s]:%d", address, portFirst+i)
			} else {
				newAddr = fmt.Sprintf("%s:%d", address, portFirst+i)
			}
			newProxyAddr.Addr = append(newProxyAddr.Addr, newAddr)
		}
	}

	return newProxyAddr, nil
}
