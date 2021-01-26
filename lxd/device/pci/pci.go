package pci

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
)

// Device represents info about a PCI uevent device.
type Device struct {
	ID       string
	SlotName string
	Driver   string
}

// ParseUeventFile returns the PCI device info for a given uevent file.
func ParseUeventFile(ueventFilePath string) (Device, error) {
	dev := Device{}

	file, err := os.Open(ueventFilePath)
	if err != nil {
		return dev, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// Looking for something like this "PCI_SLOT_NAME=0000:05:10.0"
		fields := strings.SplitN(scanner.Text(), "=", 2)
		if len(fields) == 2 {
			if fields[0] == "PCI_SLOT_NAME" {
				dev.SlotName = fields[1]
			} else if fields[0] == "PCI_ID" {
				dev.ID = fields[1]
			} else if fields[0] == "DRIVER" {
				dev.Driver = fields[1]
			}
		}
	}

	err = scanner.Err()
	if err != nil {
		return dev, err
	}

	if dev.SlotName == "" {
		return dev, fmt.Errorf("Device uevent file could not be parsed")
	}

	return dev, nil
}

// DeviceUnbind unbinds a PCI device from the OS using its PCI Slot Name.
func DeviceUnbind(pciDev Device) error {
	driverUnbindPath := fmt.Sprintf("/sys/bus/pci/devices/%s/driver/unbind", pciDev.SlotName)
	err := ioutil.WriteFile(driverUnbindPath, []byte(pciDev.SlotName), 0600)
	if err != nil {
		if !os.IsNotExist(err) || !shared.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s/", pciDev.SlotName)) {
			return errors.Wrapf(err, "Failed unbinding device %q via %q", pciDev.SlotName, driverUnbindPath)
		}
	}

	return nil
}

// DeviceSetDriverOverride registers an override driver for a PCI device using its PCI Slot Name.
func DeviceSetDriverOverride(pciDev Device, driverOverride string) error {
	overridePath := filepath.Join("/sys/bus/pci/devices", pciDev.SlotName, "driver_override")

	// The "\n" at end is important to allow the driver override to be cleared by passing "" in.
	err := ioutil.WriteFile(overridePath, []byte(fmt.Sprintf("%s\n", driverOverride)), 0600)
	if err != nil {
		return errors.Wrapf(err, "Failed setting driver override %q for device %q via %q", driverOverride, pciDev.SlotName, overridePath)
	}

	return nil
}

// DeviceProbe probes a PCI device using its PCI Slot Name.
func DeviceProbe(pciDev Device) error {
	driveProbePath := "/sys/bus/pci/drivers_probe"
	err := ioutil.WriteFile(driveProbePath, []byte(pciDev.SlotName), 0600)
	if err != nil {
		return errors.Wrapf(err, "Failed probing device %q via %q", pciDev.SlotName, driveProbePath)
	}

	return nil
}

// DeviceDriverOverride unbinds the device, sets the driver override preference, then probes the device, and
// waits for it to be activated with the specified driver.
func DeviceDriverOverride(pciDev Device, driverOverride string) error {
	revert := revert.New()
	defer revert.Fail()

	// Unbind the device from the host (ignore if not bound).
	err := DeviceUnbind(pciDev)
	if err != nil && os.IsNotExist(err) {
		return err
	}

	revert.Add(func() {
		// Reset the driver override and rebind to original driver (if needed).
		DeviceUnbind(pciDev)
		DeviceSetDriverOverride(pciDev, pciDev.Driver)
		DeviceProbe(pciDev)
	})

	// Set driver override.
	err = DeviceSetDriverOverride(pciDev, driverOverride)
	if err != nil {
		return err
	}

	// Probe device to bind it to overridden driver.
	err = DeviceProbe(pciDev)
	if err != nil {
		return err
	}

	vfioDev := Device{
		Driver:   driverOverride,
		SlotName: pciDev.SlotName,
	}

	// Wait for the device to be bound to the overridden driver if specified.
	if vfioDev.Driver != "" {
		err = deviceProbeWait(vfioDev)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// deviceProbeWait waits for PCI device to be activated with the specified driver after being probed.
func deviceProbeWait(pciDev Device) error {
	driverPath := fmt.Sprintf("/sys/bus/pci/drivers/%s/%s", pciDev.Driver, pciDev.SlotName)

	for i := 0; i < 10; i++ {
		if shared.PathExists(driverPath) {
			return nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("Device took too long to activate at %q", driverPath)
}
