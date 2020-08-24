package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

func readUint(path string) (uint64, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return 0, err
	}

	return value, nil
}

func networkAutoAttach(cluster *db.Cluster, devName string) error {
	_, dbInfo, err := cluster.GetNetworkWithInterface(devName)
	if err != nil {
		// No match found, move on
		return nil
	}

	return network.AttachInterface(dbInfo.Name, devName)
}

// networkUpdateForkdnsServersTask runs every 30s and refreshes the forkdns servers list.
func networkUpdateForkdnsServersTask(s *state.State, heartbeatData *cluster.APIHeartbeat) error {
	// Get a list of managed networks
	networks, err := s.Cluster.GetNonPendingNetworks()
	if err != nil {
		return err
	}

	for _, name := range networks {
		n, err := network.LoadByName(s, name)
		if err != nil {
			logger.Errorf("Failed to load network %q for heartbeat", name)
			continue
		}

		if n.Type() == "bridge" && n.Config()["bridge.mode"] == "fan" {
			err := n.HandleHeartbeat(heartbeatData)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func networkGetState(netIf net.Interface) api.NetworkState {
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

	network := api.NetworkState{
		Addresses: []api.NetworkStateAddress{},
		Counters:  api.NetworkStateCounters{},
		Hwaddr:    netIf.HardwareAddr.String(),
		Mtu:       netIf.MTU,
		State:     netState,
		Type:      netType,
	}

	// Populate address information.
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

			address := api.NetworkStateAddress{}
			address.Family = family
			address.Address = fields[0]
			address.Netmask = fields[1]
			address.Scope = scope

			network.Addresses = append(network.Addresses, address)
		}
	}

	// Populate bond details.
	bondPath := fmt.Sprintf("/sys/class/net/%s/bonding", netIf.Name)
	if shared.PathExists(bondPath) {
		bonding := api.NetworkStateBond{}

		// Bond mode.
		strValue, err := ioutil.ReadFile(filepath.Join(bondPath, "mode"))
		if err == nil {
			bonding.Mode = strings.Split(strings.TrimSpace(string(strValue)), " ")[0]
		}

		// Bond transmit policy.
		strValue, err = ioutil.ReadFile(filepath.Join(bondPath, "xmit_hash_policy"))
		if err == nil {
			bonding.TransmitPolicy = strings.Split(strings.TrimSpace(string(strValue)), " ")[0]
		}

		// Up delay.
		uintValue, err := readUint(filepath.Join(bondPath, "updelay"))
		if err == nil {
			bonding.UpDelay = uintValue
		}

		// Down delay.
		uintValue, err = readUint(filepath.Join(bondPath, "downdelay"))
		if err == nil {
			bonding.DownDelay = uintValue
		}

		// MII frequency.
		uintValue, err = readUint(filepath.Join(bondPath, "miimon"))
		if err == nil {
			bonding.MIIFrequency = uintValue
		}

		// MII state.
		strValue, err = ioutil.ReadFile(filepath.Join(bondPath, "mii_status"))
		if err == nil {
			bonding.MIIState = strings.TrimSpace(string(strValue))
		}

		// Lower devices.
		strValue, err = ioutil.ReadFile(filepath.Join(bondPath, "slaves"))
		if err == nil {
			bonding.LowerDevices = strings.Split(strings.TrimSpace(string(strValue)), " ")
		}

		network.Bond = &bonding
	}

	// Populate bridge details.
	bridgePath := fmt.Sprintf("/sys/class/net/%s/bridge", netIf.Name)
	if shared.PathExists(bridgePath) {
		bridge := api.NetworkStateBridge{}

		// Bridge ID.
		strValue, err := ioutil.ReadFile(filepath.Join(bridgePath, "bridge_id"))
		if err == nil {
			bridge.ID = strings.TrimSpace(string(strValue))
		}

		// Bridge STP.
		uintValue, err := readUint(filepath.Join(bridgePath, "stp_state"))
		if err == nil {
			bridge.STP = uintValue == 1
		}

		// Bridge forward delay.
		uintValue, err = readUint(filepath.Join(bridgePath, "forward_delay"))
		if err == nil {
			bridge.ForwardDelay = uintValue
		}

		// Bridge default VLAN.
		uintValue, err = readUint(filepath.Join(bridgePath, "default_pvid"))
		if err == nil {
			bridge.VLANDefault = uintValue
		}

		// Bridge VLAN filtering.
		uintValue, err = readUint(filepath.Join(bridgePath, "vlan_filtering"))
		if err == nil {
			bridge.VLANFiltering = uintValue == 1
		}

		// Upper devices.
		bridgeIfPath := fmt.Sprintf("/sys/class/net/%s/brif", netIf.Name)
		if shared.PathExists(bridgeIfPath) {
			entries, err := ioutil.ReadDir(bridgeIfPath)
			if err == nil {
				bridge.UpperDevices = []string{}
				for _, entry := range entries {
					bridge.UpperDevices = append(bridge.UpperDevices, entry.Name())
				}
			}
		}

		network.Bridge = &bridge
	}

	// Get counters.
	network.Counters = shared.NetworkGetCounters(netIf.Name)
	return network
}
