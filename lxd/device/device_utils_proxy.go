package device

import (
	"fmt"
	"net"
	"strings"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/validate"
)

// ProxyParseAddr validates a proxy address and parses it into its constituent parts.
func ProxyParseAddr(data string) (*deviceConfig.ProxyAddress, error) {
	// Split into <protocol> and <address>.
	fields := strings.SplitN(data, ":", 2)

	if !shared.StringInSlice(fields[0], []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Unknown protocol type %q", fields[0])
	}

	if len(fields) < 2 || fields[1] == "" {
		return nil, fmt.Errorf("Missing address")
	}

	newProxyAddr := &deviceConfig.ProxyAddress{
		ConnType: fields[0],
		Abstract: strings.HasPrefix(fields[1], "@"),
	}

	// unix addresses cannot have ports.
	if newProxyAddr.ConnType == "unix" {
		newProxyAddr.Address = fields[1]

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

	newProxyAddr.Address = address

	// Split <ports> into individual ports and port ranges.
	ports := strings.SplitN(port, ",", -1)

	newProxyAddr.Ports = make([]uint64, 0, len(ports))

	for _, p := range ports {
		portFirst, portRange, err := network.ParsePortRange(p)
		if err != nil {
			return nil, err
		}

		for i := int64(0); i < portRange; i++ {
			newProxyAddr.Ports = append(newProxyAddr.Ports, uint64(portFirst+i))
		}
	}

	if len(newProxyAddr.Ports) <= 0 {
		return nil, fmt.Errorf("At least one port is required")
	}

	return newProxyAddr, nil
}
