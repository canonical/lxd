package ip

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"

	"github.com/lxc/lxd/shared"
)

// Link represents base arguments for link device
type Link struct {
	Name   string
	MTU    string
	Parent string
}

// args generate common arguments for the virtual link
func (l *Link) args(linkType string) []string {
	var result []string
	if l.Parent != "" {
		result = append(result, "link", l.Parent)
	}
	if l.MTU != "" {
		result = append(result, "mtu", l.MTU)
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

// SetMTU sets the MTU of the link device
func (l *Link) SetMTU(mtu string) error {
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

// VirtFuncInfo holds information about vf.
type VirtFuncInfo struct {
	VF         int              `json:"vf"`
	Address    string           `json:"address"`
	MAC        string           `json:"mac"` // Deprecated
	VLANs      []map[string]int `json:"vlan_list"`
	SpoofCheck bool             `json:"spoofchk"`
}

// GetVFInfo returns info about virtual function
func (l *Link) GetVFInfo(vfID int) (VirtFuncInfo, error) {
	vf := VirtFuncInfo{}
	vfNotFoundErr := fmt.Errorf("no matching virtual function found")

	ipPath, err := exec.LookPath("ip")
	if err != nil {
		return vf, fmt.Errorf("ip command not found")
	}

	// Function to get VF info using regex matching, for older versions of ip tool. Less reliable.
	vfFindByRegex := func(devName string, vfID int) (VirtFuncInfo, error) {
		cmd := exec.Command(ipPath, "link", "show", devName)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return vf, err
		}
		defer func() { _ = stdout.Close() }()

		err = cmd.Start()
		if err != nil {
			return vf, err
		}
		defer func() { _ = cmd.Wait() }()

		// Try and match: "vf 1 MAC 00:00:00:00:00:00, vlan 4095, spoof checking off"
		reVlan := regexp.MustCompile(fmt.Sprintf(`vf %d MAC ((?:[[:xdigit:]]{2}:){5}[[:xdigit:]]{2}).*, vlan (\d+), spoof checking (\w+)`, vfID))

		// IP link command doesn't show the vlan property if its set to 0, so we need to detect that.
		// Try and match: "vf 1 MAC 00:00:00:00:00:00, spoof checking off"
		reNoVlan := regexp.MustCompile(fmt.Sprintf(`vf %d MAC ((?:[[:xdigit:]]{2}:){5}[[:xdigit:]]{2}).*, spoof checking (\w+)`, vfID))
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			// First try and find VF and read its properties with VLAN activated.
			res := reVlan.FindStringSubmatch(scanner.Text())
			if len(res) == 4 {
				vlan, err := strconv.Atoi(res[2])
				if err != nil {
					return vf, err
				}

				vf.Address = res[1]
				vf.VLANs = append(vf.VLANs, map[string]int{"vlan": vlan})
				vf.SpoofCheck = shared.IsTrue(res[3])

				return vf, err
			}

			// Next try and find VF and read its properties with VLAN missing.
			res = reNoVlan.FindStringSubmatch(scanner.Text())
			if len(res) == 3 {
				vf.Address = res[1]
				// Missing VLAN ID means 0 when resetting later.
				vf.VLANs = append(vf.VLANs, map[string]int{"vlan": 0})
				vf.SpoofCheck = shared.IsTrue(res[2])

				return vf, err
			}
		}

		err = scanner.Err()
		if err != nil {
			return vf, err
		}

		return vf, vfNotFoundErr
	}

	// First try using the JSON output format as is more reliable to parse.
	cmd := exec.Command(ipPath, "-j", "link", "show", l.Name)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return vf, err
	}
	defer func() { _ = stdout.Close() }()

	err = cmd.Start()
	if err != nil {
		return vf, err
	}
	defer func() { _ = cmd.Wait() }()

	// Temporary struct to decode ip output into.
	var ifInfo []struct {
		VFList []VirtFuncInfo `json:"vfinfo_list"`
	}

	// Decode JSON output.
	dec := json.NewDecoder(stdout)
	err = dec.Decode(&ifInfo)
	if err != nil && err != io.EOF {
		return vf, err
	}

	err = cmd.Wait()
	if err != nil {
		// If JSON command fails, fallback to using regex match mode for older versions of ip tool.
		// This does not support the newer VF "link/ether" output prefix.
		return vfFindByRegex(l.Name, vfID)
	}

	if len(ifInfo) == 0 {
		return vf, vfNotFoundErr
	}

	// Search VFs returned for match.
	found := false
	for _, vfInfo := range ifInfo[0].VFList {
		if vfInfo.VF == vfID {
			vf = vfInfo // Found a match.
			found = true
		}
	}

	if !found {
		return vf, vfNotFoundErr
	}

	// Always populate VLANs slice if not already populated. Missing VLAN ID means 0 when resetting later.
	if len(vf.VLANs) == 0 {
		vf.VLANs = append(vf.VLANs, map[string]int{"vlan": 0})
	}

	// Ensure empty VLAN entry is consistently populated.
	if _, found = vf.VLANs[0]["vlan"]; !found {
		vf.VLANs[0]["vlan"] = 0
	}

	// If ip tool has provided old mac field, copy into newer address field.
	if vf.MAC != "" && vf.Address == "" {
		vf.Address = vf.MAC
	}

	return vf, nil
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

// BridgeVLANAdd adds a new vlan filter entry
func (l *Link) BridgeVLANAdd(vid string, pvid bool, untagged bool, self bool, master bool) error {
	cmd := []string{"vlan", "add", "dev", l.Name, "vid", vid}

	if pvid {
		cmd = append(cmd, "pvid")
	}

	if untagged {
		cmd = append(cmd, "untagged")
	}

	if self {
		cmd = append(cmd, "self")
	}

	if master {
		cmd = append(cmd, "master")
	}

	_, err := shared.RunCommand("bridge", cmd...)
	if err != nil {
		return err
	}
	return nil
}

// BridgeVLANDelete removes an existing vlan filter entry
func (l *Link) BridgeVLANDelete(vid string, self bool, master bool) error {
	cmd := []string{"vlan", "del", "dev", l.Name, "vid", vid}

	if self {
		cmd = append(cmd, "self")
	}

	if master {
		cmd = append(cmd, "master")
	}

	_, err := shared.RunCommand("bridge", cmd...)
	if err != nil {
		return err
	}
	return nil
}

// BridgeLinkSetIsolated sets bridge 'isolated' attribute on a port
func (l *Link) BridgeLinkSetIsolated(isolated bool) error {
	isolatedState := "on"
	if !isolated {
		isolatedState = "off"
	}

	_, err := shared.RunCommand("bridge", "link", "set", "dev", l.Name, "isolated", isolatedState)
	if err != nil {
		return err
	}
	return nil
}

// BridgeLinkSetHairpin sets bridge 'hairpin' attribute on a port
func (l *Link) BridgeLinkSetHairpin(hairpin bool) error {
	hairpinState := "on"
	if !hairpin {
		hairpinState = "off"
	}

	_, err := shared.RunCommand("bridge", "link", "set", "dev", l.Name, "hairpin", hairpinState)
	if err != nil {
		return err
	}
	return nil
}
