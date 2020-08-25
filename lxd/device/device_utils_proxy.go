package device

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/validate"
)

// ProxyParseAddr validates a proxy address and parses it into its constituent parts.
func ProxyParseAddr(addr string) (*deviceConfig.ProxyAddress, error) {
	// Split into <protocol> and <address>.
	fields := strings.SplitN(addr, ":", 2)

	if !shared.StringInSlice(fields[0], []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Unknown connection type '%s'", fields[0])
	}

	newProxyAddr := &deviceConfig.ProxyAddress{
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
		err := validate.Optional(validate.IsNetworkAddress)(address)
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
			newAddr := net.JoinHostPort(address, strconv.Itoa(int(portFirst+i)))
			newProxyAddr.Addr = append(newProxyAddr.Addr, newAddr)
		}
	}

	return newProxyAddr, nil
}
