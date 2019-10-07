package resources

import (
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/jaypipes/pcidb"
	"github.com/pkg/errors"
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
			return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "sriov_totalvfs"))
		}

		vfCurrent, err := readUint(filepath.Join(devicePath, "sriov_numvfs"))
		if err != nil {
			return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "sriov_numvfs"))
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
			return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "numa_node"))
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
			return errors.Wrapf(err, "Failed to read \"%s\"", deviceVendorPath)
		}

		card.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
	}

	deviceDevicePath := filepath.Join(devicePath, "device")
	if sysfsExists(deviceDevicePath) {
		id, err := ioutil.ReadFile(deviceDevicePath)
		if err != nil {
			return errors.Wrapf(err, "Failed to read \"%s\"", deviceDevicePath)
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
			return errors.Wrapf(err, "Failed to track down \"%s\"", driverPath)
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
			return errors.Wrapf(err, "Failed to list \"%s\"", netPath)
		}

		// Iterate and record port data
		for _, entry := range entries {
			interfacePath := filepath.Join(netPath, entry.Name())
			info := &api.ResourcesNetworkCardPort{
				ID: entry.Name(),
			}

			// Add type
			if sysfsExists(filepath.Join(interfacePath, "type")) {
				devType, err := readUint(filepath.Join(interfacePath, "type"))
				if err != nil {
					return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(interfacePath, "type"))
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
					return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(interfacePath, "address"))
				}

				info.Address = strings.TrimSpace(string(address))
			}

			// Add port number
			if sysfsExists(filepath.Join(interfacePath, "dev_port")) {
				port, err := readUint(filepath.Join(interfacePath, "dev_port"))
				if err != nil {
					return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(interfacePath, "dev_port"))
				}

				info.Port = port
			}

			// Add infiniband specific information
			if info.Protocol == "infiniband" && sysfsExists(filepath.Join(devicePath, "infiniband")) {
				infiniband := &api.ResourcesNetworkCardPortInfiniband{}

				madPath := filepath.Join(devicePath, "infiniband_mad")
				if sysfsExists(madPath) {
					ibPort := info.Port + 1

					entries, err := ioutil.ReadDir(madPath)
					if err != nil {
						return errors.Wrapf(err, "Failed to list \"%s\"", madPath)
					}

					for _, entry := range entries {
						entryName := entry.Name()
						currentPort, err := readUint(filepath.Join(madPath, entryName, "port"))
						if err != nil {
							return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(madPath, entryName, "port"))
						}

						if currentPort != ibPort {
							continue
						}

						if !sysfsExists(filepath.Join(madPath, entryName, "dev")) {
							continue
						}

						dev, err := ioutil.ReadFile(filepath.Join(madPath, entryName, "dev"))
						if err != nil {
							return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(madPath, entryName, "dev"))
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
				if sysfsExists(verbsPath) {
					entries, err := ioutil.ReadDir(verbsPath)
					if err != nil {
						return errors.Wrapf(err, "Failed to list \"%s\"", verbsPath)
					}

					if len(entries) == 1 {
						verbName := entries[0].Name()
						infiniband.VerbName = verbName

						if !sysfsExists(filepath.Join(verbsPath, verbName, "dev")) {
							continue
						}

						dev, err := ioutil.ReadFile(filepath.Join(verbsPath, verbName, "dev"))
						if err != nil {
							return errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(verbsPath, verbName, "dev"))
						}

						infiniband.VerbDevice = strings.TrimSpace(string(dev))
					}
				}

				info.Infiniband = infiniband
			}

			// Attempt to add ethtool details (ignore failures)
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Getting physical port info for VFs makes no sense
				card.Ports = append(card.Ports, *info)
				continue
			}

			ethtoolAddPortInfo(info)

			card.Ports = append(card.Ports, *info)
		}

		if len(card.Ports) > 0 {
			ethtoolAddCardInfo(card.Ports[0].ID, card)
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
		return nil, errors.Wrap(err, "Failed to get uname")
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
			return nil, errors.Wrapf(err, "Failed to list \"%s\"", sysClassNet)
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
				return nil, errors.Wrapf(err, "Failed to track down \"%s\"", devicePath)
			}

			if strings.HasPrefix(linkTarget, "/sys/devices/pci") && sysfsExists(filepath.Join(devicePath, "subsystem")) {
				virtio := strings.HasPrefix(filepath.Base(linkTarget), "virtio")
				if virtio {
					linkTarget = filepath.Dir(linkTarget)
				}

				subsystem, err := filepath.EvalSymlinks(filepath.Join(devicePath, "subsystem"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to track down \"%s\"", filepath.Join(devicePath, "subsystem"))
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
				return nil, errors.Wrapf(err, "Failed to add device information for \"%s\"", devicePath)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to track down \"%s\"", filepath.Join(devicePath, "physfn"))
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
			return nil, errors.Wrapf(err, "Failed to list \"%s\"", sysBusPci)
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
				return nil, errors.Wrapf(err, "Failed to read \"%s\"", filepath.Join(devicePath, "class"))
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
				return nil, errors.Wrapf(err, "Failed to add device information for \"%s\"", devicePath)
			}

			// Add to list
			if sysfsExists(filepath.Join(devicePath, "physfn")) {
				// Virtual functions need to be added to the parent
				linkTarget, err := filepath.EvalSymlinks(filepath.Join(devicePath, "physfn"))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to track down \"%s\"", filepath.Join(devicePath, "physfn"))
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
