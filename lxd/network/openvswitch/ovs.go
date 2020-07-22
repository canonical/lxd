package openvswitch

import (
	"os/exec"

	"github.com/lxc/lxd/shared"
)

// NewOVS initialises new OVS wrapper.
func NewOVS() *OVS {
	return &OVS{}
}

// OVS command wrapper.
type OVS struct{}

// Installed returns true if OVS tools are installed.
func (o *OVS) Installed() bool {
	_, err := exec.LookPath("ovs-vsctl")
	if err != nil {
		return false
	}

	return true
}

// BridgeExists returns true if OVS bridge exists.
func (o *OVS) BridgeExists(bridgeName string) (bool, error) {
	_, err := shared.RunCommand("ovs-vsctl", "br-exists", bridgeName)
	if err != nil {
		runErr, ok := err.(shared.RunError)
		if ok {
			exitError, ok := runErr.Err.(*exec.ExitError)

			// ovs-vsctl manpage says that br-exists exits with code 2 if bridge doesn't exist.
			if ok && exitError.ExitCode() == 2 {
				return false, nil
			}
		}

		return false, err
	}

	return true, nil
}

// BridgeAdd adds an OVS bridge.
func (o *OVS) BridgeAdd(bridgeName string) error {
	_, err := shared.RunCommand("ovs-vsctl", "add-br", bridgeName)
	if err != nil {
		return err
	}

	return nil
}

// BridgeDelete deletes an OVS bridge.
func (o *OVS) BridgeDelete(bridgeName string) error {
	_, err := shared.RunCommand("ovs-vsctl", "del-br", bridgeName)
	if err != nil {
		return err
	}

	return nil
}

// BridgeAddPort adds a port to the bridge (if already attached does nothing).
func (o *OVS) BridgeAddPort(bridgeName string, portName string) error {
	// Check if interface is already connected to a bridge, if not, connect it to the specified bridge.
	_, err := shared.RunCommand("ovs-vsctl", "port-to-br", portName)
	if err != nil {
		_, err := shared.RunCommand("ovs-vsctl", "add-port", bridgeName, portName)
		if err != nil {
			return err
		}
	}

	return nil
}

// BridgeDeletePort deletes a port from the bridge (if already deteached does nothing).
func (o *OVS) BridgeDeletePort(bridgeName string, portName string) error {
	// Check if interface is connected to a bridge, if so, then remove it from the bridge.
	_, err := shared.RunCommand("ovs-vsctl", "port-to-br", portName)
	if err == nil {
		_, err := shared.RunCommand("ovs-vsctl", "del-port", bridgeName, portName)
		if err != nil {
			return err
		}
	}

	return nil
}

// PortSet sets port options.
func (o *OVS) PortSet(portName string, options ...string) error {
	_, err := shared.RunCommand("ovs-vsctl", append([]string{"set", "port", portName}, options...)...)
	if err != nil {
		return err
	}

	return nil
}
