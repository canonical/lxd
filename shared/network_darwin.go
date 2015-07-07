// +build darwin

package shared

import (
	"net"
	"os/exec"
)

// IsBridge detect if the given interface is a bridge.
func IsBridge(iface *net.Interface) bool {
	err := exec.Command("ifconfig", iface.Name, "addr").Run()
	return err == nil
}
