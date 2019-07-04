package resources

import (
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/jaypipes/pcidb"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
)

var sysClassNet = "/sys/class/net"

var netProtocols = map[uint64]string{
	1:  "ethernet",
	19: "ATM",
	32: "infiniband",
}

func networkAddDeviceInfo(devicePath string, pciDB *pcidb.PCIDB, uname unix.Utsname, card *api.ResourcesNetworkCard) error {
	// SRIOV
	if sysfsExists(filepath.Join(devicePath, "sriov_numvfs")) {
		sriov := api.ResourcesNetworkCardSRIOV{}

		// Get maximum and current VF count
		vfMaximum, err := readUint(filepath.Join(devicePath, "sriov_totalvfs"))
		if err != nil {
			return err
		}

		vfCurrent, err := readUint(filepath.Join(devicePath, "sriov_numvfs"))
		if err != nil {
			return err
		}

		sriov.MaximumVFs = vfMaximum
		sriov.CurrentVFs = vfCurrent

		// Add the SRIOV data to the card
		card.SRIOV = &sriov
	}

	// NUMA node
	if sysfsExists(filepath.Join(devicePath, "numa_node")) {
		numaNode, err := readInt(filepath.Join(devicePath, "numa_node"))
		if err != nil {
			return err
		}

		if numaNode > 0 {
			card.NUMANode = uint64(numaNode)
		}
	}

	// Vendor and product
	deviceVendorPath := filepath.Join(devicePath, "vendor")
	if sysfsExists(deviceVendorPath) {
		id, err := ioutil.ReadFile(deviceVendorPath)
		if err != nil {
			return err
		}

		card.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	deviceDevicePath := filepath.Join(devicePath, "device")
	if sysfsExists(deviceDevicePath) {
		id, err := ioutil.ReadFile(deviceDevicePath)
		if err != nil {
			return err
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
	driverPath := filepath.Join(devicePath, "driver")
	if sysfsExists(driverPath) {
		linkTarget, err := filepath.EvalSymlinks(driverPath)
		if err != nil {
			return err
		}

		// Set the driver name
		card.Driver = filepath.Base(linkTarget)

		// Try to get the version, fallback to kernel version
		out, err := ioutil.ReadFile(filepath.Join(driverPath, "module", "version"))
		if err == nil {
			card.DriverVersion = strings.TrimSpace(string(out))
		} else {
			card.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
		}
	}

	// Port information
	netPath := filepath.Join(devicePath, "net")
	if sysfsExists(netPath) {
		card.Ports = []api.ResourcesNetworkCardPort{}

		entries, err := ioutil.ReadDir(netPath)
		if err != nil {
			return err
		}

		// Iterate and record port data
		for _, entry := range entries {
			interfacePath := filepath.Join(netPath, entry.Name())
			info := &api.ResourcesNetworkCardPort{
				ID: entry.Name(),
			}

			// Add type
			if sysfsExists(filepath.Join(interfacepath, "type")) {
				devType, err := readUint(filepath.Join(interfacePath, "type"))
				if err != nil {
					return err
				}

				protocol, ok := netProtocols[devType]
				if !ok {
					info.Protocol = "unknown"
				}

				info.Protocol = protocol
			}

			// Add MAC address
			if sysfsExists(filepath.Join(interfacePath, "address")) {
				address, err := ioutil.ReadFile(filepath.Join(interfacePath, "address"))
				if err != nil {
					return err
				}

				info.Address = strings.TrimSpace(string(address))
			}

			// Add port number
			if sysfsExists(filepath.Join(interfacePath, "dev_port")) {
				port, err := readUint(filepath.Join(interfacePath, "dev_port"))
				if err != nil {
					return err
				}

				info.Port = port
			}

			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Getting physical port info for VFs makes no sense
				card.Ports = append(card.Ports, *info)
				continue
			}

			// Attempt to add ethtool details (ignore failures)
			ethtoolAddInfo(info)

			card.Ports = append(card.Ports, *info)
		}
	}

	return nil
}

// GetNetwork returns a filled api.ResourcesNetwork struct ready for use by LXD
func GetNetwork() (*api.ResourcesNetwork, error) {
	network := api.ResourcesNetwork{}
	network.Cards = []api.ResourcesNetworkCard{}

	// Get uname for driver version
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, err
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
	if sysfsExists(sysClassNet) {
		entries, err := ioutil.ReadDir(sysClassNet)
		if err != nil {
			return nil, err
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			entryPath := filepath.Join(sysClassNet, entryName)
			devicePath := filepath.Join(entryPath, "device")

			// Only keep physical network devices
			if !sysfsExists(filepath.Join(entryPath, "device")) {
				continue
			}

			// Setup the entry
			card := api.ResourcesNetworkCard{}

			// PCI address
			linkTarget, err := filepath.EvalSymlinks(devicePath)
			if err != nil {
				return nil, err
			}

			if strings.HasPrefix(linkTarget, "/sys/devices/pci") && sysfsExists(filepath.Join(devicePath, "subsystem")) {
				virtio := strings.HasPrefix(filepath.Base(linkTarget), "virtio")
				if virtio {
					linkTarget = filepath.Dir(linkTarget)
				}

				subsystem, err := filepath.EvalSymlinks(filepath.Join(devicePath, "subsystem"))
				if err != nil {
					return nil, err
				}

				if filepath.Base(subsystem) == "pci" || virtio {
					card.PCIAddress = filepath.Base(linkTarget)

					// Skip devices we already know about
					if stringInSlice(card.PCIAddress, pciKnown) {
						continue
					}

					pciKnown = append(pciKnown, card.PCIAddress)
				}
			}

			// Add device information for PFs
			err = networkAddDeviceInfo(devicePath, pciDB, uname, &card)
			if err != nil {
				return nil, err
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, err
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
	if sysfsExists(sysBusPci) {
		entries, err := ioutil.ReadDir(sysBusPci)
		if err != nil {
			return nil, err
		}

		// Iterate and add to our list
		for _, entry := range entries {
			entryName := entry.Name()
			devicePath := filepath.Join(sysBusPci, entryName)

			// Skip devices we already know about
			if stringInSlice(entryName, pciKnown) {
				continue
			}

			// Only care about identifiable devices
			if !sysfsExists(filepath.Join(devicePath, "class")) {
				continue
			}

			class, err := ioutil.ReadFile(filepath.Join(devicePath, "class"))
			if err != nil {
				return nil, err
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
				return nil, err
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, err
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
