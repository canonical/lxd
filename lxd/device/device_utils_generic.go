package device

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

// deviceJoinPath joins together prefix and text delimited by a "." for device path generation.
func deviceJoinPath(parts ...string) string {
	return strings.Join(parts, ".")
}

// pciDevice represents info about a PCI uevent device.
type pciDevice struct {
	ID       string
	SlotName string
	Driver   string
}

// pciParseUeventFile returns the PCI device info for a given uevent file.
func pciParseUeventFile(ueventFilePath string) (pciDevice, error) {
	dev := pciDevice{}

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

// pciDeviceUnbind unbinds a PCI device from the OS using its PCI Slot Name.
func pciDeviceUnbind(pciDev pciDevice) error {
	driverUnbindPath := fmt.Sprintf("/sys/bus/pci/devices/%s/driver/unbind", pciDev.SlotName)
	err := ioutil.WriteFile(driverUnbindPath, []byte(pciDev.SlotName), 0600)
	if err != nil {
		return errors.Wrapf(err, "Failed unbinding device %q via %q", pciDev.SlotName, driverUnbindPath)
	}

	return nil
}

// pciDeviceSetDriverOverride registers an override driver for a PCI device using its PCI Slot Name.
func pciDeviceSetDriverOverride(pciDev pciDevice, driverOverride string) error {
	overridePath := filepath.Join("/sys/bus/pci/devices", pciDev.SlotName, "driver_override")

	// The "\n" at end is important to allow the driver override to be cleared by passing "" in.
	err := ioutil.WriteFile(overridePath, []byte(fmt.Sprintf("%s\n", driverOverride)), 0600)
	if err != nil {
		return errors.Wrapf(err, "Failed setting driver override %q for device %q via %q", driverOverride, pciDev.SlotName, overridePath)
	}

	return nil
}

// pciDeviceProbe probes a PCI device using its PCI Slot Name.
func pciDeviceProbe(pciDev pciDevice) error {
	driveProbePath := "/sys/bus/pci/drivers_probe"
	err := ioutil.WriteFile(driveProbePath, []byte(pciDev.SlotName), 0600)
	if err != nil {
		return errors.Wrapf(err, "Failed probing device %q via %q", pciDev.SlotName, driveProbePath)
	}

	return nil
}

// pciDeviceProbeWait waits for PCI device to be activated with the specified driver after being probed.
func pciDeviceProbeWait(pciDev pciDevice) error {
	driverPath := fmt.Sprintf("/sys/bus/pci/drivers/%s/%s", pciDev.Driver, pciDev.SlotName)

	for i := 0; i < 10; i++ {
		if shared.PathExists(driverPath) {
			return nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("Device took too long to activate at %q", driverPath)
}

// pciDeviceDriverOverride unbinds the device, sets the driver override preference, then probes the device, and
// waits for it to be activated with the specified driver.
func pciDeviceDriverOverride(pciDev pciDevice, driverOverride string) error {
	revert := revert.New()
	defer revert.Fail()

	// Unbind the device from the host (ignore if not bound).
	err := pciDeviceUnbind(pciDev)
	if err != nil && os.IsNotExist(err) {
		return err
	}

	revert.Add(func() {
		// Reset the driver override and rebind to original driver (if needed).
		pciDeviceUnbind(pciDev)
		pciDeviceSetDriverOverride(pciDev, pciDev.Driver)
		pciDeviceProbe(pciDev)
	})

	// Set driver override.
	err = pciDeviceSetDriverOverride(pciDev, driverOverride)
	if err != nil {
		return err
	}

	// Probe device to bind it to overridden driver.
	err = pciDeviceProbe(pciDev)
	if err != nil {
		return err
	}

	vfioDev := pciDevice{
		Driver:   driverOverride,
		SlotName: pciDev.SlotName,
	}

	// Wait for the device to be bound to the overridden driver if specified.
	if vfioDev.Driver != "" {
		err = pciDeviceProbeWait(vfioDev)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}
