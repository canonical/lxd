package device

import (
	"fmt"
	"strings"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared"
)

// deviceJoinPath joins together prefix and text delimited by a "." for device path generation.
func deviceJoinPath(parts ...string) string {
	return strings.Join(parts, ".")
}

// validatePCIDevice returns whether a configured PCI device exists. It also returns true, if no device
// has been specified.
func validatePCIDevice(config deviceConfig.Device) error {
	if config["pci"] != "" && !shared.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s", (config["pci"]))) {
		return fmt.Errorf("Invalid PCI address (no device found): %s", config["pci"])
	}

	return nil
}
