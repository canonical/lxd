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
func (o *OVS) BridgeAdd(bridgeName string, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "add-br", bridgeName)

	_, err := shared.RunCommand("ovs-vsctl", args...)
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

// BridgePortAdd adds a port to the bridge (if already attached does nothing).
func (o *OVS) BridgePortAdd(bridgeName string, portName string, mayExist bool) error {
	args := []string{}

	if mayExist {
		args = append(args, "--may-exist")
	}

	args = append(args, "add-port", bridgeName, portName)

	_, err := shared.RunCommand("ovs-vsctl", args...)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortDelete deletes a port from the bridge (if already detached does nothing).
func (o *OVS) BridgePortDelete(bridgeName string, portName string) error {
	_, err := shared.RunCommand("ovs-vsctl", "--if-exists", "del-port", bridgeName, portName)
	if err != nil {
		return err
	}

	return nil
}

// BridgePortSet sets port options.
func (o *OVS) BridgePortSet(portName string, options ...string) error {
	_, err := shared.RunCommand("ovs-vsctl", append([]string{"set", "port", portName}, options...)...)
	if err != nil {
		return err
	}

	return nil
}
