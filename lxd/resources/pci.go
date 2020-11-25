package resources

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jaypipes/pcidb"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
)

// GetPCI returns a filled api.ResourcesPCI struct ready for use by LXD
func GetPCI() (*api.ResourcesPCI, error) {
	pci := api.ResourcesPCI{}

	if !sysfsExists(sysBusPci) {
		return &pci, nil
	}

	// Load PCI database
	pciDB, err := pcidb.New()
	if err != nil {
		pciDB = nil
	}

	// Get uname for driver version
	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get uname")
	}

	// List all PCI devices
	entries, err := ioutil.ReadDir(sysBusPci)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to list %q", sysBusPci)
	}

	for _, entry := range entries {
		entryName := entry.Name()
		devicePath := filepath.Join(sysBusPci, entryName)
		device := api.ResourcesPCIDevice{}

		// Get driver name
		driverPath := filepath.Join(devicePath, "driver")

		if sysfsExists(driverPath) {
			linkTarget, err := filepath.EvalSymlinks(driverPath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to get driver of %q", devicePath)
			}

			device.Driver = filepath.Base(linkTarget)

			// Try to get the version, fallback to kernel version
			out, err := ioutil.ReadFile(filepath.Join(driverPath, "module", "version"))
			if err == nil {
				device.DriverVersion = strings.TrimSpace(string(out))
			} else {
				device.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
			}
		}

		// Get NUMA node
		if sysfsExists(filepath.Join(devicePath, "numa_node")) {
			numaNode, err := readInt(filepath.Join(devicePath, "numa_node"))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", filepath.Join(devicePath, "numa_node"))
			}

			if numaNode > 0 {
				device.NUMANode = uint64(numaNode)
			}
		}

		// Get PCI address
		device.PCIAddress = entryName

		// Get product ID node
		deviceDevicePath := filepath.Join(devicePath, "device")
		if sysfsExists(deviceDevicePath) {
			id, err := ioutil.ReadFile(deviceDevicePath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", deviceDevicePath)
			}

			device.ProductID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
		}

		// Get vendor ID node
		deviceVendorPath := filepath.Join(devicePath, "vendor")
		if sysfsExists(deviceVendorPath) {
			id, err := ioutil.ReadFile(deviceVendorPath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", deviceVendorPath)
			}

			device.VendorID = strings.TrimPrefix(strings.TrimSpace(string(id)), "0x")
		}

		// Fill vendor and product names
		if pciDB != nil {
			vendor, ok := pciDB.Vendors[device.VendorID]
			if ok {
				device.Vendor = vendor.Name

				for _, product := range vendor.Products {
					if product.ID == device.ProductID {
						device.Product = product.Name
						break
					}
				}
			}
		}

		//Get IOMMU Group
		iommuGroupSymPath := filepath.Join(sysBusPci, device.PCIAddress, "iommu_group")
		if sysfsExists(iommuGroupSymPath) {
			iommuGroupPath, err := os.Readlink(iommuGroupSymPath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to readlink %q", iommuGroupSymPath)
			}

			iommuGroup := filepath.Base(iommuGroupPath)
			device.IOMMUGroup, err = strconv.ParseUint(iommuGroup, 10, 64)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to parse %q", iommuGroup)
			}
		} else {
			device.IOMMUGroup = 0
		}

		pci.Devices = append(pci.Devices, device)
		pci.Total++
	}

	return &pci, nil
}
