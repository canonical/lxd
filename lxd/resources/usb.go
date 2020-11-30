package resources

import (
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/usbid"
)

var sysBusUSB = "/sys/bus/usb/devices"

// GetUSB returns a filled api.ResourcesUSB struct ready for use by LXD
func GetUSB() (*api.ResourcesUSB, error) {
	// Load the USB database.
	usbid.Load()

	usb := api.ResourcesUSB{}

	if !sysfsExists(sysBusUSB) {
		return &usb, nil
	}

	// List all USB devices
	entries, err := ioutil.ReadDir(sysBusUSB)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to list %q", sysBusUSB)
	}

	// Get uname for driver version
	uname := unix.Utsname{}
	err = unix.Uname(&uname)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get uname")
	}

	for _, entry := range entries {
		entryName := entry.Name()
		devicePath := filepath.Join(sysBusUSB, entryName)

		// Skip entries without a bus address
		if !sysfsExists(filepath.Join(devicePath, "busnum")) {
			continue
		}

		devClassFile := filepath.Join(devicePath, "bDeviceClass")
		if sysfsExists(devClassFile) {
			content, err := ioutil.ReadFile(devClassFile)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", devClassFile)
			}

			devClass, err := strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to parse device class %q", content)
			}

			// Skip USB hubs
			if devClass == 9 {
				continue
			}
		}

		device := api.ResourcesUSBDevice{}

		// Get bus address
		device.BusAddress, err = readUint(filepath.Join(devicePath, "busnum"))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to read %q", filepath.Join(devicePath, "busnum"))
		}

		// Get device address
		device.DeviceAddress, err = readUint(filepath.Join(devicePath, "devnum"))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to read %q", filepath.Join(devicePath, "devnum"))
		}

		// Get product ID
		var productID uint64

		deviceProductIDPath := filepath.Join(devicePath, "idProduct")
		if sysfsExists(deviceProductIDPath) {
			content, err := ioutil.ReadFile(deviceProductIDPath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", deviceProductIDPath)
			}

			device.ProductID = strings.TrimPrefix(strings.TrimSpace(string(content)), "0x")

			productID, err = strconv.ParseUint(device.ProductID, 16, 64)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to parse product ID %q", device.ProductID)
			}
		}

		// Get vendor ID
		var vendorID uint64

		deviceVendorIDPath := filepath.Join(devicePath, "idVendor")
		if sysfsExists(deviceVendorIDPath) {
			content, err := ioutil.ReadFile(deviceVendorIDPath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", deviceVendorIDPath)
			}

			device.VendorID = strings.TrimPrefix(strings.TrimSpace(string(content)), "0x")

			vendorID, err = strconv.ParseUint(device.VendorID, 16, 64)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to parse vendor ID %q", device.VendorID)
			}
		}

		// Get vendor and product name
		deviceProductPath := filepath.Join(devicePath, "product")
		if sysfsExists(deviceProductPath) {
			content, err := ioutil.ReadFile(deviceProductPath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", deviceProductPath)
			}

			device.Product = strings.TrimSpace(string(content))
		}

		vendor := usbid.Vendors[usbid.ID(vendorID)]
		if vendor != nil {
			device.Vendor = vendor.Name

			// If there's no product file, get it from usbid.
			if device.Product == "" {
				product := vendor.Product[usbid.ID(productID)]
				if product != nil {
					device.Product = product.Name
				}
			}
		}

		// Get speed
		deviceSpeedPath := filepath.Join(devicePath, "speed")
		if sysfsExists(deviceSpeedPath) {
			content, err := ioutil.ReadFile(deviceSpeedPath)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to read %q", deviceSpeedPath)
			}

			device.Speed, err = strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to parse speed %q", content)
			}
		}

		// List USB interfaces
		subEntries, err := ioutil.ReadDir(devicePath)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to list %q", devicePath)
		}

		for _, subEntry := range subEntries {
			subEntryName := subEntry.Name()
			subDevicePath := filepath.Join(devicePath, subEntryName)

			// Skip irrelevant directories and file entries
			if !subEntry.IsDir() || !strings.HasPrefix(subEntryName, entryName) {
				continue
			}

			iface := api.ResourcesUSBDeviceInterface{}

			// Get class ID
			var class *usbid.Class

			interfaceClassPath := filepath.Join(subDevicePath, "bInterfaceClass")
			if sysfsExists(interfaceClassPath) {
				content, err := ioutil.ReadFile(interfaceClassPath)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read %q", interfaceClassPath)
				}

				iface.ClassID, err = strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to parse class ID %q", content)
				}

				var ok bool

				class, ok = usbid.Classes[usbid.ClassCode(iface.ClassID)]
				if ok {
					iface.Class = class.Name
				}
			}

			// Get subclass ID
			interfaceSubClassPath := filepath.Join(subDevicePath, "bInterfaceSubClass")
			if sysfsExists(interfaceSubClassPath) {
				content, err := ioutil.ReadFile(interfaceSubClassPath)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read %q", interfaceSubClassPath)
				}

				iface.SubClassID, err = strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to parse subclass ID %q", content)
				}

				if iface.SubClassID > 0 && class != nil {
					subclass, ok := class.SubClass[usbid.ClassCode(iface.SubClassID)]
					if ok {
						iface.SubClass = subclass.Name
					}
				}
			}

			// Get number
			interfaceNumber := filepath.Join(subDevicePath, "bInterfaceNumber")
			if sysfsExists(interfaceNumber) {
				content, err := ioutil.ReadFile(interfaceNumber)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to read %q", interfaceNumber)
				}

				iface.Number, err = strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to parse interface number %q", content)
				}
			}

			// Get driver
			driverPath := filepath.Join(subDevicePath, "driver")
			if sysfsExists(driverPath) {
				linkTarget, err := filepath.EvalSymlinks(driverPath)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to get driver of %q", subDevicePath)
				}

				iface.Driver = filepath.Base(linkTarget)

				// Try to get the version, fallback to kernel version
				out, err := ioutil.ReadFile(filepath.Join(driverPath, "module", "version"))
				if err == nil {
					iface.DriverVersion = strings.TrimSpace(string(out))
				} else {
					iface.DriverVersion = strings.TrimRight(string(uname.Release[:]), "\x00")
				}
			}

			device.Interfaces = append(device.Interfaces, iface)
		}

		usb.Devices = append(usb.Devices, device)
		usb.Total++
	}

	return &usb, nil
}
