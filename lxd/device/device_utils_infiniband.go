package device

import (
	"fmt"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

// IBDevPrefix Infiniband devices prefix.
const IBDevPrefix = "infiniband.unix"

// infinibandDevices extracts the infiniband parent device from the supplied nic list and any free
// associated virtual functions (VFs) that are on the same card and port as the specified parent.
// This function expects that the supplied nic list does not include VFs that are already attached
// to running instances.
func infinibandDevices(nics *api.ResourcesNetwork, parent string) map[string]*api.ResourcesNetworkCardPort {
	ibDevs := make(map[string]*api.ResourcesNetworkCardPort)
	for _, card := range nics.Cards {
		for _, port := range card.Ports {
			// Skip non-infiniband ports.
			if port.Protocol != "infiniband" {
				continue
			}

			// Skip port if not parent.
			if port.ID != parent {
				continue
			}

			// Store infiniband port info.
			ibDevs[port.ID] = &port
		}

		// Skip virtual function (VF) extraction if SRIOV isn't supported on port.
		if card.SRIOV == nil {
			continue
		}

		// Record if parent has been found as a physical function (PF).
		parentDev, parentIsPF := ibDevs[parent]

		for _, VF := range card.SRIOV.VFs {
			for _, port := range VF.Ports {
				// Skip non-infiniband VFs.
				if port.Protocol != "infiniband" {
					continue
				}

				// Skip VF if parent is a PF and VF is not on same port as parent.
				if parentIsPF && parentDev.Port != port.Port {
					continue
				}

				// Skip VF if parent isn't a PF and VF doesn't match parent name.
				if !parentIsPF && port.ID != parent {
					continue
				}

				// Store infiniband VF port info.
				ibDevs[port.ID] = &port
			}
		}
	}

	return ibDevs
}

// infinibandAddDevices creates the UNIX devices for the provided IBF device and then configures the
// supplied runConfig with the Cgroup rules and mount instructions to pass the device into instance.
func infinibandAddDevices(s *state.State, devicesPath string, deviceName string, ibDev *api.ResourcesNetworkCardPort, runConf *RunConfig) error {
	if ibDev.Infiniband == nil {
		return fmt.Errorf("No infiniband devices supplied")
	}

	// Add IsSM device if defined.
	if ibDev.Infiniband.IsSMName != "" {
		dummyDevice := config.Device{
			"source": fmt.Sprintf("/dev/infiniband/%s", ibDev.Infiniband.IsSMName),
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, dummyDevice, false, runConf)
		if err != nil {
			return err
		}
	}

	// Add MAD device if defined.
	if ibDev.Infiniband.MADName != "" {
		dummyDevice := config.Device{
			"source": fmt.Sprintf("/dev/infiniband/%s", ibDev.Infiniband.MADName),
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, dummyDevice, false, runConf)
		if err != nil {
			return err
		}
	}

	// Add Verb device if defined.
	if ibDev.Infiniband.VerbName != "" {
		dummyDevice := config.Device{
			"source": fmt.Sprintf("/dev/infiniband/%s", ibDev.Infiniband.VerbName),
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, dummyDevice, false, runConf)
		if err != nil {
			return err
		}
	}

	return nil
}
