package ip

import (
	"github.com/lxc/lxd/shared"
)

// Link represents base arguments for link device
type Link struct {
	Name   string
	Mtu    string
	Parent string
}

// args generate common arguments for the virtual link
func (l *Link) args(linkType string) []string {
	var result []string
	if l.Parent != "" {
		result = append(result, "link", l.Parent)
	}
	if l.Mtu != "" {
		result = append(result, "mtu", l.Mtu)
	}
	result = append(result, "type", linkType)
	return result
}

// add adds new virtual link
func (l *Link) add(linkType string, additionalArgs []string) error {
	cmd := []string{"link", "add", l.Name}
	cmd = append(cmd, l.args(linkType)...)
	cmd = append(cmd, additionalArgs...)
	_, err := shared.RunCommand("ip", cmd...)
	if err != nil {
		return err
	}
	return nil
}

// SetUp enables the link device
func (l *Link) SetUp() error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "up")
	if err != nil {
		return err
	}
	return nil
}

// SetDown disables the link device
func (l *Link) SetDown() error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "down")
	if err != nil {
		return err
	}
	return nil
}

// SetMtu sets the mtu of the link device
func (l *Link) SetMtu(mtu string) error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "mtu", mtu)
	if err != nil {
		return err
	}
	return nil
}

// SetAddress sets the address of the link device
func (l *Link) SetAddress(address string) error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "address", address)
	if err != nil {
		return err
	}
	return nil
}

// SetMaster sets the master of the link device
func (l *Link) SetMaster(master string) error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "master", master)
	if err != nil {
		return err
	}
	return nil
}

// SetNoMaster removes the master of the link device
func (l *Link) SetNoMaster() error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "nomaster")
	if err != nil {
		return err
	}
	return nil
}

// SetName sets the name of the link device
func (l *Link) SetName(newName string) error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "name", newName)
	if err != nil {
		return err
	}
	return nil
}

// SetNetns moves the link to the selected network namespace
func (l *Link) SetNetns(netns string) error {
	_, err := shared.RunCommand("ip", "link", "set", "dev", l.Name, "netns", netns)
	if err != nil {
		return err
	}
	return nil
}

// SetVfAddress changes the address for the specified vf
func (l *Link) SetVfAddress(vf string, address string) error {
	_, err := shared.TryRunCommand("ip", "link", "set", "dev", l.Name, "vf", vf, "mac", address)
	if err != nil {
		return err
	}
	return nil
}

// SetVfVlan changes the assigned VLAN for the specified vf
func (l *Link) SetVfVlan(vf string, vlan string) error {
	_, err := shared.TryRunCommand("ip", "link", "set", "dev", l.Name, "vf", vf, "vlan", vlan)
	if err != nil {
		return err
	}
	return nil
}

// SetVfSpoofchk turns packet spoof checking on or off for the specified VF
func (l *Link) SetVfSpoofchk(vf string, mode string) error {
	_, err := shared.TryRunCommand("ip", "link", "set", "dev", l.Name, "vf", vf, "spoofchk", mode)
	if err != nil {
		return err
	}
	return nil
}

// Change sets map for link device
func (l *Link) Change(devType string, fanMap string) error {
	_, err := shared.RunCommand("ip", "link", "change", "dev", l.Name, "type", devType, "fan-map", fanMap)
	if err != nil {
		return err
	}
	return nil
}

// Delete deletes the link device
func (l *Link) Delete() error {
	_, err := shared.RunCommand("ip", "link", "delete", "dev", l.Name)
	if err != nil {
		return err
	}
	return nil
}
