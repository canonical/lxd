package resources

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jaypipes/pcidb"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/network/openvswitch"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

var sysClassNet = "/sys/class/net"

var netProtocols = map[uint64]string{
	1:  "ethernet",
	19: "ATM",
	32: "infiniband",
}

func networkAddDeviceInfo(devicePath string, pciDB *pcidb.PCIDB, uname unix.Utsname, card *api.ResourcesNetworkCard) error {
	deviceDeviceDir, err := getDeviceDir(devicePath)
	if err != nil {
		return fmt.Errorf("Failed to read %q: %w", devicePath, err)
	}

	// VDPA
	vDPAMatches, err := filepath.Glob(filepath.Join(deviceDeviceDir, "vdpa*"))
	if err != nil {
		return fmt.Errorf("Malformed VDPA device name search pattern: %w", err)
	}

	if len(vDPAMatches) > 0 {
		vdpa := api.ResourcesNetworkCardVDPA{}

		splittedPath := strings.Split(vDPAMatches[0], "/")
		vdpa.Name = splittedPath[len(splittedPath)-1]
		vDPADevMatches, err := filepath.Glob(filepath.Join(vDPAMatches[0], "vhost-vdpa-*"))
		if err != nil {
			return fmt.Errorf("Malformed VDPA device name search pattern: %w", err)
		}

		if len(vDPADevMatches) == 0 {
			return fmt.Errorf("Failed to find VDPA device at device path %q", vDPAMatches[0])
		}

		splittedPath = strings.Split(vDPADevMatches[0], "/")
		vdpa.Device = splittedPath[len(splittedPath)-1]

		// Add the VDPA data to the card
		card.VDPA = &vdpa
	}

	// SRIOV
	sriovNumVFsPath := filepath.Join(deviceDeviceDir, "sriov_numvfs")
	if pathExists(sriovNumVFsPath) {
		sriov := api.ResourcesNetworkCardSRIOV{}

		// Get maximum and current VF count
		sriovTotalVFsPath := filepath.Join(deviceDeviceDir, "sriov_totalvfs")
		vfMaximum, err := readUint(sriovTotalVFsPath)
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", sriovTotalVFsPath, err)
		}

		vfCurrent, err := readUint(sriovNumVFsPath)
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", sriovNumVFsPath, err)
		}

		sriov.MaximumVFs = vfMaximum
		sriov.CurrentVFs = vfCurrent

		// Add the SRIOV data to the card
		card.SRIOV = &sriov
	}

	// NUMA node
	numaNodePath := filepath.Join(deviceDeviceDir, "numa_node")
	if pathExists(numaNodePath) {
		numaNode, err := readInt(numaNodePath)
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", numaNodePath, err)
		}

		if numaNode > 0 {
			card.NUMANode = uint64(numaNode)
		}
	}

	// USB address
	usbAddr, err := usbAddress(deviceDeviceDir)
	if err != nil {
		return fmt.Errorf("Failed to find USB address for %q: %w", deviceDeviceDir, err)
	}

	if usbAddr != "" {
		card.USBAddress = usbAddr
	}

	// Vendor and product
	deviceVendorPath := filepath.Join(deviceDeviceDir, "vendor")
	if pathExists(deviceVendorPath) {
		id, err := os.ReadFile(deviceVendorPath)
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", deviceVendorPath, err)
		}

		card.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	deviceDevicePath := filepath.Join(deviceDeviceDir, "device")
	if pathExists(deviceDevicePath) {
		id, err := os.ReadFile(deviceDevicePath)
		if err != nil {
			return fmt.Errorf("Failed to read %q: %w", deviceDevicePath, err)
		}

		card.ProductID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	// Fill vendor and product names
	if pciDB != nil {
		vendor, ok := pciDB.Vendors[card.VendorID]
		if ok {
			card.Vendor = vendor.Name

			for _, product := range vendor.Products {
				if product.ID == card.ProductID {
					card.Product = product.Name
					break
				}
			}
		}
	}

	// Driver information
	driverPath := filepath.Join(deviceDeviceDir, "driver")
	if pathExists(driverPath) {
		linkTarget, err := filepath.EvalSymlinks(driverPath)
		if err != nil {
			return fmt.Errorf("Failed to find device directory %q: %w", driverPath, err)
		}

		// Set the driver name
		card.Driver = filepath.Base(linkTarget)

		// Try to get the version, fallback to kernel version
		out, err := os.ReadFile(filepath.Join(driverPath, "module", "version"))
		if err == nil {
			card.DriverVersion = strings.TrimSpace(string(out))
		} else {
			card.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
		}
	}

	// Port information
	netPath := filepath.Join(devicePath, "net")
	if pathExists(netPath) {
		card.Ports = []api.ResourcesNetworkCardPort{}

		entries, err := os.ReadDir(netPath)
		if err != nil {
			return fmt.Errorf("Failed to list %q: %w", netPath, err)
		}

		// Iterate and record port data
		for _, entry := range entries {
			interfacePath := filepath.Join(netPath, entry.Name())
			info := &api.ResourcesNetworkCardPort{
				ID: entry.Name(),
			}

			// Add type
			typePath := filepath.Join(interfacePath, "type")
			if pathExists(typePath) {
				devType, err := readUint(typePath)
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", typePath, err)
				}

				protocol, ok := netProtocols[devType]
				if !ok {
					info.Protocol = "unknown"
				} else {
					info.Protocol = protocol
				}
			}

			// Add MAC address
			addressPath := filepath.Join(interfacePath, "address")
			if info.Address == "" && pathExists(addressPath) {
				address, err := os.ReadFile(addressPath)
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", addressPath, err)
				}

				info.Address = strings.TrimSpace(string(address))
			}

			// Add port number
			devPortPath := filepath.Join(interfacePath, "dev_port")
			if pathExists(devPortPath) {
				port, err := readUint(devPortPath)
				if err != nil {
					return fmt.Errorf("Failed to read %q: %w", devPortPath, err)
				}

				info.Port = port
			}

			// Add infiniband specific information
			if info.Protocol == "infiniband" && pathExists(filepath.Join(devicePath, "infiniband")) {
				infiniband := &api.ResourcesNetworkCardPortInfiniband{}

				madPath := filepath.Join(devicePath, "infiniband_mad")
				if pathExists(madPath) {
					ibPort := info.Port + 1

					entries, err := os.ReadDir(madPath)
					if err != nil {
						return fmt.Errorf("Failed to list %q: %w", madPath, err)
					}

					for _, entry := range entries {
						entryName := entry.Name()
						madEntryPath := filepath.Join(madPath, entryName)
						madEntryPortPath := filepath.Join(madEntryPath, "port")
						currentPort, err := readUint(madEntryPortPath)
						if err != nil {
							return fmt.Errorf("Failed to read %q: %w", madEntryPortPath, err)
						}

						if currentPort != ibPort {
							continue
						}

						madEntryDevPath := filepath.Join(madEntryPath, "dev")
						if !pathExists(madEntryDevPath) {
							continue
						}

						dev, err := os.ReadFile(madEntryDevPath)
						if err != nil {
							return fmt.Errorf("Failed to read %q: %w", madEntryDevPath, err)
						}

						if strings.HasPrefix(entryName, "issm") {
							infiniband.IsSMName = entryName
							infiniband.IsSMDevice = strings.TrimSpace(string(dev))
						}

						if strings.HasPrefix(entryName, "umad") {
							infiniband.MADName = entryName
							infiniband.MADDevice = strings.TrimSpace(string(dev))
						}
					}
				}

				verbsPath := filepath.Join(devicePath, "infiniband_verbs")
				if pathExists(verbsPath) {
					entries, err := os.ReadDir(verbsPath)
					if err != nil {
						return fmt.Errorf("Failed to list %q: %w", verbsPath, err)
					}

					if len(entries) == 1 {
						verbName := entries[0].Name()
						infiniband.VerbName = verbName

						verbDevPath := filepath.Join(verbsPath, verbName, "dev")
						if !pathExists(verbDevPath) {
							continue
						}

						dev, err := os.ReadFile(verbDevPath)
						if err != nil {
							return fmt.Errorf("Failed to read %q: %w", verbDevPath, err)
						}

						infiniband.VerbDevice = strings.TrimSpace(string(dev))
					}
				}

				info.Infiniband = infiniband
			}

			if pathExists(filepath.Join(devicePath, "physfn")) {
				// Getting physical port info for VFs makes no sense
				card.Ports = append(card.Ports, *info)
				continue
			}

			// Attempt to add ethtool details (ignore failures)
			err = ethtoolAddPortInfo(info)
			if err != nil {
				continue
			}

			card.Ports = append(card.Ports, *info)
		}

		if len(card.Ports) > 0 {
			err = ethtoolAddCardInfo(card.Ports[0].ID, card)
			if err != nil {
				return fmt.Errorf("Failed to add card info: %w", err)
			}
		}
	}

	return nil
}

// GetNetwork returns a filled api.ResourcesNetwork struct ready for use by LXD.
func GetNetwork() (*api.ResourcesNetwork, error) {
	network := api.ResourcesNetwork{}
	network.Cards = []api.ResourcesNetworkCard{}

	// Get uname for driver version
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, fmt.Errorf("Failed to get uname: %w", err)
	}

	// Load PCI database
	pciDB, err := pcidb.New()
	if err != nil {
		pciDB = nil
	}

	// Temporary variables
	pciKnown := []string{}
	pciVFs := map[string][]api.ResourcesNetworkCard{}

	// Detect all Networks available through kernel network interface
	if pathExists(sysClassNet) {
		entries, err := os.ReadDir(sysClassNet)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysClassNet, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysClassNet, entryName)
			devicePath := filepath.Join(entryPath, "device")

			// Only keep physical network devices
			if !pathExists(devicePath) {
				continue
			}

			// Setup the entry
			card := api.ResourcesNetworkCard{}

			// PCI address.
			pciAddr, err := pciAddress(devicePath)
			if err != nil {
				return nil, fmt.Errorf("Failed to find PCI address for %q: %w", devicePath, err)
			}

			if pciAddr != "" {
				card.PCIAddress = pciAddr

				// Skip devices we already know about
				if slices.Contains(pciKnown, card.PCIAddress) {
					continue
				}

				pciKnown = append(pciKnown, card.PCIAddress)
			}

			// Add device information for PFs
			err = networkAddDeviceInfo(devicePath, pciDB, uname, &card)
			if err != nil {
				return nil, fmt.Errorf("Failed to add device information for %q: %w", devicePath, err)
			}

			// Add to list
			physfnPath := filepath.Join(devicePath, "physfn")
			if pathExists(physfnPath) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(physfnPath)
				if err != nil {
					return nil, fmt.Errorf("Failed to find %q: %w", physfnPath, err)
				}

				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesNetworkCard{}
				}

				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				network.Cards = append(network.Cards, card)
			}
		}
	}

	// Detect remaining Networks on PCI bus
	if pathExists(sysBusPci) {
		entries, err := os.ReadDir(sysBusPci)
		if err != nil {
			return nil, fmt.Errorf("Failed to list %q: %w", sysBusPci, err)
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			devicePath := filepath.Join(sysBusPci, entryName)

			// Skip devices we already know about
			if slices.Contains(pciKnown, entryName) {
				continue
			}

			// Only care about identifiable devices
			classPath := filepath.Join(devicePath, "class")
			if !pathExists(classPath) {
				continue
			}

			class, err := os.ReadFile(classPath)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %q: %w", classPath, err)
			}

			// Only care about VGA devices
			if !strings.HasPrefix(string(class), "0x02") {
				continue
			}

			// Start building up data
			card := api.ResourcesNetworkCard{}
			card.PCIAddress = entryName

			// Add device information
			err = networkAddDeviceInfo(devicePath, pciDB, uname, &card)
			if err != nil {
				return nil, fmt.Errorf("Failed to add device information for %q: %w", devicePath, err)
			}

			// Add to list
			physfnPath := filepath.Join(devicePath, "physfn")
			if pathExists(physfnPath) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(physfnPath)
				if err != nil {
					return nil, fmt.Errorf("Failed to find %q: %w", physfnPath, err)
				}

				parentAddress := filepath.Base(linkTarget)

				_, ok := pciVFs[parentAddress]
				if !ok {
					pciVFs[parentAddress] = []api.ResourcesNetworkCard{}
				}

				pciVFs[parentAddress] = append(pciVFs[parentAddress], card)
			} else {
				network.Cards = append(network.Cards, card)
			}
		}
	}

	// Add SRIOV devices and count devices
	network.Total = 0
	for _, card := range network.Cards {
		if card.SRIOV != nil {
			card.SRIOV.VFs = pciVFs[card.PCIAddress]
			network.Total += uint64(len(card.SRIOV.VFs))
		}

		network.Total++
	}

	return &network, nil
}

// Fetch native linux bridge information.
func getNativeBridgeState(bridgePath string, name string) *api.NetworkStateBridge {
	bridge := api.NetworkStateBridge{}
	// Bridge ID.
	strValue, err := os.ReadFile(filepath.Join(bridgePath, "bridge_id"))
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
	bridgeIfPath := fmt.Sprintf("/sys/class/net/%s/brif", name)
	if pathExists(bridgeIfPath) {
		entries, err := os.ReadDir(bridgeIfPath)
		if err == nil {
			bridge.UpperDevices = []string{}
			for _, entry := range entries {
				bridge.UpperDevices = append(bridge.UpperDevices, entry.Name())
			}
		}
	}

	return &bridge
}

// Fetch OVS bridge information.
// Returns nil if interface is not an OVS bridge.
func getOVSBridgeState(name string) *api.NetworkStateBridge {
	ovs := openvswitch.NewOVS()
	isOVSBridge := false
	if ovs.Installed() {
		isOVSBridge, _ = ovs.BridgeExists(name)
	}

	if !isOVSBridge {
		return nil
	}

	bridge := api.NetworkStateBridge{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
	defer cancel()

	// Bridge ID
	strValue, err := ovs.GenerateOVSBridgeID(ctx, name)
	if err == nil {
		bridge.ID = strValue
	}

	// Bridge STP
	boolValue, err := ovs.STPEnabled(ctx, name)
	if err == nil {
		bridge.STP = boolValue
	}

	// Bridge Forwards Delay
	uintValue, err := ovs.GetSTPForwardDelay(ctx, name)
	if err == nil {
		bridge.ForwardDelay = uintValue
	}

	// Bridge default VLAN (PVID)
	uintValue, err = ovs.GetVLANPVID(ctx, name)
	if err == nil {
		bridge.VLANDefault = uintValue
	}

	// Bridge VLAN filtering
	boolValue, err = ovs.VLANFilteringEnabled(ctx, name)
	if err == nil {
		bridge.VLANFiltering = boolValue
	}

	// Upper devices
	entries, err := ovs.BridgePortList(name)
	if err == nil {
		bridge.UpperDevices = append(bridge.UpperDevices, entries...)
	}

	return &bridge
}

// GetNetworkState returns the OS configuration for the network interface.
func GetNetworkState(name string) (*api.NetworkState, error) {
	// Reject known bad names that might cause problem when dealing with paths.
	err := validate.IsInterfaceName(name)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Invalid network interface name %q: %v", name, err)
	}

	// Get some information
	netIf, err := net.InterfaceByName(name)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "Network interface %q not found", name)
	}

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
			address, netmask, found := strings.Cut(addr.String(), "/")
			if !found {
				continue
			}

			family := "inet"
			if strings.Contains(address, ":") {
				family = "inet6"
			}

			networkAddress := api.NetworkStateAddress{
				Family:  family,
				Address: address,
				Netmask: netmask,
				Scope:   shared.GetIPScope(address),
			}

			network.Addresses = append(network.Addresses, networkAddress)
		}
	}

	// Populate bond details.
	bondPath := fmt.Sprintf("/sys/class/net/%s/bonding", name)
	if pathExists(bondPath) {
		bonding := api.NetworkStateBond{}

		// Bond mode.
		strValue, err := os.ReadFile(filepath.Join(bondPath, "mode"))
		if err == nil {
			bonding.Mode = strings.Split(strings.TrimSpace(string(strValue)), " ")[0]
		}

		// Bond transmit policy.
		strValue, err = os.ReadFile(filepath.Join(bondPath, "xmit_hash_policy"))
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
		strValue, err = os.ReadFile(filepath.Join(bondPath, "mii_status"))
		if err == nil {
			bonding.MIIState = strings.TrimSpace(string(strValue))
		}

		// Lower devices.
		strValue, err = os.ReadFile(filepath.Join(bondPath, "slaves"))
		if err == nil {
			bonding.LowerDevices = strings.Split(strings.TrimSpace(string(strValue)), " ")
		}

		network.Bond = &bonding
	}

	// Populate bridge details
	bridgePath := fmt.Sprintf("/sys/class/net/%s/bridge", name)
	if pathExists(bridgePath) {
		network.Bridge = getNativeBridgeState(bridgePath, name)
	} else {
		network.Bridge = getOVSBridgeState(name)
	}

	// Populate VLAN details.
	type vlan struct {
		lower string
		vid   uint64
	}

	vlans := map[string]vlan{}

	vlanPath := "/proc/net/vlan/config"
	if pathExists(vlanPath) {
		entries, err := os.ReadFile(vlanPath)
		if err != nil {
			return nil, err
		}

		for line := range strings.SplitSeq(string(entries), "\n") {
			fields := strings.Split(line, "|")
			if len(fields) != 3 {
				continue
			}

			vName := strings.TrimSpace(fields[0])
			vVID, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
			if err != nil {
				continue
			}

			vLower := strings.TrimSpace(fields[2])

			vlans[vName] = vlan{
				lower: vLower,
				vid:   vVID,
			}
		}
	}

	// Check if the interface is a VLAN.
	entry, ok := vlans[name]
	if ok {
		network.VLAN = &api.NetworkStateVLAN{
			LowerDevice: entry.lower,
			VID:         entry.vid,
		}
	}

	// Get counters.
	counters, err := GetNetworkCounters(name)
	if err != nil {
		return nil, err
	}

	network.Counters = *counters

	return &network, nil
}

// GetNetworkCounters returns the current packet counters for the network interface.
func GetNetworkCounters(name string) (*api.NetworkStateCounters, error) {
	counters := api.NetworkStateCounters{}

	// Get counters
	content, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		if os.IsNotExist(err) {
			return &counters, nil
		}

		return nil, err
	}

	// A sample line:
	// eth0: 1024 0 0 0 0 0 0 0 2048 0 0 0 0 0 0 0
	for line := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 17 {
			continue
		}

		if fields[0] != name+":" {
			continue
		}

		rxBytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, err
		}

		rxPackets, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return nil, err
		}

		txBytes, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, err
		}

		txPackets, err := strconv.ParseUint(fields[10], 10, 64)
		if err != nil {
			return nil, err
		}

		counters.BytesSent = txBytes
		counters.BytesReceived = rxBytes
		counters.PacketsSent = txPackets
		counters.PacketsReceived = rxPackets
		break
	}

	return &counters, nil
}
