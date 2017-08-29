package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared/api"
)

func cmdForkGetNet(args *Args) error {
	networks := map[string]api.ContainerStateNetwork{}

	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	stats := map[string][]int64{}

	content, err := ioutil.ReadFile("/proc/net/dev")
	if err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			fields := strings.Fields(line)

			if len(fields) != 17 {
				continue
			}

			rxBytes, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				continue
			}

			rxPackets, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				continue
			}

			txBytes, err := strconv.ParseInt(fields[9], 10, 64)
			if err != nil {
				continue
			}

			txPackets, err := strconv.ParseInt(fields[10], 10, 64)
			if err != nil {
				continue
			}

			intName := strings.TrimSuffix(fields[0], ":")
			stats[intName] = []int64{rxBytes, rxPackets, txBytes, txPackets}
		}
	}

	for _, netIf := range interfaces {
		netState := "down"
		netType := "unknown"

		if netIf.Flags&net.FlagBroadcast > 0 {
			netType = "broadcast"
		}

		if netIf.Flags&net.FlagPointToPoint > 0 {
			netType = "point-to-point"
		}

		if netIf.Flags&net.FlagLoopback > 0 {
			netType = "loopback"
		}

		if netIf.Flags&net.FlagUp > 0 {
			netState = "up"
		}

		network := api.ContainerStateNetwork{
			Addresses: []api.ContainerStateNetworkAddress{},
			Counters:  api.ContainerStateNetworkCounters{},
			Hwaddr:    netIf.HardwareAddr.String(),
			Mtu:       netIf.MTU,
			State:     netState,
			Type:      netType,
		}

		addrs, err := netIf.Addrs()
		if err == nil {
			for _, addr := range addrs {
				fields := strings.SplitN(addr.String(), "/", 2)
				if len(fields) != 2 {
					continue
				}

				family := "inet"
				if strings.Contains(fields[0], ":") {
					family = "inet6"
				}

				scope := "global"
				if strings.HasPrefix(fields[0], "127") {
					scope = "local"
				}

				if fields[0] == "::1" {
					scope = "local"
				}

				if strings.HasPrefix(fields[0], "169.254") {
					scope = "link"
				}

				if strings.HasPrefix(fields[0], "fe80:") {
					scope = "link"
				}

				address := api.ContainerStateNetworkAddress{}
				address.Family = family
				address.Address = fields[0]
				address.Netmask = fields[1]
				address.Scope = scope

				network.Addresses = append(network.Addresses, address)
			}
		}

		counters, ok := stats[netIf.Name]
		if ok {
			network.Counters.BytesReceived = counters[0]
			network.Counters.PacketsReceived = counters[1]
			network.Counters.BytesSent = counters[2]
			network.Counters.PacketsSent = counters[3]
		}

		networks[netIf.Name] = network
	}

	buf, err := json.Marshal(networks)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", buf)

	return nil
}
