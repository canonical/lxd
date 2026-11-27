package device

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/ip"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/validate"
)

const (
	// nicP2PBridgeAliasPrefix marks a host bridge as managed by a peered p2p NIC.
	nicP2PBridgeAliasPrefix = "lxd-peer-bridge:"

	// nicP2PPortAliasPrefix marks a host port as managed by a peered p2p NIC.
	nicP2PPortAliasPrefix = "lxd-peer-port:"
)

// nicP2PPeer identifies the other side of a peered p2p NIC.
type nicP2PPeer struct {
	Instance string
	Device   string
}

// String returns the peer in <instance>:<device> form.
func (p nicP2PPeer) String() string {
	return p.Instance + ":" + p.Device
}

// nicP2PParsePeer parses a peer value in <instance>:<device> form.
func nicP2PParsePeer(value string) (*nicP2PPeer, error) {
	if value == "" {
		return nil, nil
	}

	instanceName, deviceName, found := strings.Cut(value, ":")
	if !found || instanceName == "" || deviceName == "" {
		return nil, fmt.Errorf("Invalid peer %q: expected <instance>:<device>", value)
	}

	err := instancetype.ValidName(instanceName, false)
	if err != nil {
		return nil, fmt.Errorf("Invalid peer instance name %q: %w", instanceName, err)
	}

	err = validate.IsDeviceName(deviceName)
	if err != nil {
		return nil, fmt.Errorf("Invalid peer device name %q: %w", deviceName, err)
	}

	return &nicP2PPeer{
		Instance: instanceName,
		Device:   deviceName,
	}, nil
}

// nicP2PPeerNames contains the deterministic bridge name and ownership markers used for a
// peered p2p NIC. They are derived only from the LXD-generated link ID and the instance's
// own LXD-generated ID, never from user-controlled instance, project or device names.
type nicP2PPeerNames struct {
	Bridge      string
	BridgeAlias string
	PortAlias   string
}

// nicP2PPeerNamesFor returns the host bridge name and ownership markers for a peered p2p
// NIC. Both sides of the pair compute the same Bridge and BridgeAlias from the shared link
// ID, while the port alias includes the instance's own LXD-generated ID so that each side
// only ever removes its own port. The port name itself is random and stored in the
// device's volatile host_name.
func nicP2PPeerNamesFor(localInstanceID int, linkID string) nicP2PPeerNames {
	localID := strconv.Itoa(localInstanceID)

	return nicP2PPeerNames{
		Bridge:      nicP2PShortName("lxdp", "lxd-peer-bridge|"+linkID),
		BridgeAlias: nicP2PBridgeAliasPrefix + linkID,
		PortAlias:   nicP2PPortAliasPrefix + linkID + ":" + localID,
	}
}

// nicP2PPeerPortPrefix returns the prefix for the random host-side port name.
func nicP2PPeerPortPrefix(instanceType instancetype.Type) string {
	if instanceType == instancetype.VM {
		return "tap"
	}

	return "veth"
}

// nicP2PPeerLockName returns the process-local lock name for a device pair.
// It must be derived only from the project and the two device references, because both
// sides need to agree on it before a link ID has been assigned. The result is used as an
// in-process mutex key only and never to name a host interface.
func nicP2PPeerLockName(projectName string, instanceName string, deviceName string, peerRef string) (string, error) {
	peer, err := nicP2PParsePeer(peerRef)
	if err != nil {
		return "", err
	}

	refs := []string{instanceName + ":" + deviceName, peer.Instance + ":" + peer.Device}
	sort.Strings(refs)

	return "nic_p2p_peer_link|" + projectName + "|" + strings.Join(refs, "|"), nil
}

// nicP2PShortName returns a deterministic interface name no longer than IFNAMSIZ-1.
func nicP2PShortName(prefix string, seed string) string {
	hash := sha256.Sum256([]byte(seed))
	return prefix + hex.EncodeToString(hash[:])[:11]
}

// networkGetP2PLinkAlias returns the interface alias (IFLA_IFALIAS) of a link.
func networkGetP2PLinkAlias(linkName string) (string, error) {
	content, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/ifalias", linkName))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(content)), nil
}

// networkSetP2PLinkAlias sets the interface alias (IFLA_IFALIAS) of a link.
func networkSetP2PLinkAlias(linkName string, alias string) error {
	_, err := shared.RunCommand(context.TODO(), "ip", "link", "set", "dev", linkName, "alias", alias)
	if err != nil {
		return fmt.Errorf("Failed setting alias on interface %q: %w", linkName, err)
	}

	return nil
}

// networkCheckP2PLinkAlias verifies that a link carries the expected LXD-owned alias.
// This prevents a name collision from causing LXD to reconfigure or delete a host interface
// that it does not own.
func networkCheckP2PLinkAlias(linkName string, expectedAlias string) error {
	alias, err := networkGetP2PLinkAlias(linkName)
	if err != nil {
		return fmt.Errorf("Failed getting alias for interface %q: %w", linkName, err)
	}

	if alias != expectedAlias {
		return fmt.Errorf("Interface %q is not owned by this peer link", linkName)
	}

	return nil
}

// networkCreateP2PBridge creates and configures the private bridge used by a peered p2p NIC.
// The bridge is host-inert: no addresses, IPv6 disabled, fixed options and its local port is
// not a member of any VLAN. Its name and configuration are not derived from user config.
func networkCreateP2PBridge(bridgeName string, bridgeAlias string) error {
	bridgeExists := network.InterfaceExists(bridgeName)
	if bridgeExists {
		if !network.IsNativeBridge(bridgeName) {
			return fmt.Errorf("Existing interface %q is not a Linux bridge", bridgeName)
		}

		err := networkCheckP2PLinkAlias(bridgeName, bridgeAlias)
		if err != nil {
			return fmt.Errorf("Refusing to reuse interface %q: %w", bridgeName, err)
		}
	} else {
		bridge := &ip.Bridge{
			Name: bridgeName,
		}

		err := bridge.Add()
		if err != nil {
			return fmt.Errorf("Failed creating peer bridge %q: %w", bridgeName, err)
		}

		err = networkSetP2PLinkAlias(bridgeName, bridgeAlias)
		if err != nil {
			_ = network.InterfaceRemove(bridgeName)
			return err
		}
	}

	// Remove the bridge if configuring it fails. A pre-existing bridge is left in place
	// because it was not created for this pair.
	cleanup := func() {
		if !bridgeExists {
			_ = network.InterfaceRemove(bridgeName)
		}
	}

	var err error

	// Disable features that could inject host state or host-generated traffic.
	// These options are not required for forwarding, but failures (for example when
	// bridge netfilter is not available) must not prevent the bridge from being used.
	nonFatalOptions := [][]string{
		{"multicast_snooping", "0"},
		{"nf_call_iptables", "0"},
		{"nf_call_ip6tables", "0"},
		{"nf_call_arptables", "0"},
	}

	for _, option := range nonFatalOptions {
		_ = networkSetP2PBridgeOption(bridgeName, option[0], option[1])
	}

	fatalOptions := [][]string{
		{"stp_state", "0"},
		{"group_fwd_mask", "0"},
		{"vlan_default_pvid", "0"},
	}

	for _, option := range fatalOptions {
		err = networkSetP2PBridgeOption(bridgeName, option[0], option[1])
		if err != nil {
			cleanup()
			return err
		}
	}

	// Enable VLAN filtering so the host local port can be excluded from the data plane.
	err = network.BridgeVLANFilterSetStatus(bridgeName, "1")
	if err != nil {
		cleanup()
		return err
	}

	// Disable IPv6 on the bridge so it cannot acquire a link-local address or process RAs.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", bridgeName), "1")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanup()
		return fmt.Errorf("Failed disabling IPv6 on peer bridge %q: %w", bridgeName, err)
	}

	// Ensure the bridge local port is not a member of any VLAN.
	_ = (&ip.Link{Name: bridgeName}).BridgeVLANDelete("1", true)
	_ = (&ip.Link{Name: bridgeName}).BridgeVLANDelete("1-4094", true)

	err = (&ip.Link{Name: bridgeName}).SetUp()
	if err != nil {
		cleanup()
		return fmt.Errorf("Failed bringing up peer bridge %q: %w", bridgeName, err)
	}

	return nil
}

// networkSetP2PBridgeOption sets a Linux bridge option.
func networkSetP2PBridgeOption(bridgeName string, key string, value string) error {
	_, err := shared.RunCommand(context.TODO(), "ip", "link", "set", "dev", bridgeName, "type", "bridge", key, value)
	if err != nil {
		return fmt.Errorf("Failed setting bridge %q option %q to %q: %w", bridgeName, key, value, err)
	}

	return nil
}

// networkAttachP2PPort attaches an instance port to a peer bridge as a trunk port.
// VLAN 1 is the untagged native VLAN and VLANs 2-4094 are passed tagged.
func networkAttachP2PPort(bridgeName string, portName string, portAlias string) error {
	if !network.InterfaceExists(portName) {
		return fmt.Errorf("Missing peer port %q", portName)
	}

	// Never attach an interface that this peer link does not own.
	err := networkCheckP2PLinkAlias(portName, portAlias)
	if err != nil {
		return fmt.Errorf("Refusing to attach interface %q: %w", portName, err)
	}

	// Prevent the host-side port from acquiring IPv6 state.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", portName), "1")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed disabling IPv6 on peer port %q: %w", portName, err)
	}

	err = network.AttachInterface(bridgeName, portName)
	if err != nil {
		return fmt.Errorf("Failed attaching peer port %q to bridge %q: %w", portName, bridgeName, err)
	}

	link := &ip.Link{Name: portName}

	// Untagged traffic uses VLAN 1.
	err = link.BridgeVLANAdd("1", true, true, false)
	if err != nil {
		return fmt.Errorf("Failed adding native VLAN to peer port %q: %w", portName, err)
	}

	// Allow all tagged private VLANs across the link.
	err = link.BridgeVLANAdd("2-4094", false, false, false)
	if err != nil {
		return fmt.Errorf("Failed adding VLAN range to peer port %q: %w", portName, err)
	}

	// Defensively ensure the bridge local port remains outside all VLANs.
	_ = (&ip.Link{Name: bridgeName}).BridgeVLANDelete("1", true)
	_ = (&ip.Link{Name: bridgeName}).BridgeVLANDelete("1-4094", true)

	err = link.SetUp()
	if err != nil {
		return fmt.Errorf("Failed bringing up peer port %q: %w", portName, err)
	}

	return nil
}

// networkDetachP2PPort detaches and removes an instance port from a peer bridge.
// It refuses to touch an interface that does not carry the expected LXD-owned alias.
func networkDetachP2PPort(bridgeName string, portName string, portAlias string) error {
	if portName == "" || !network.InterfaceExists(portName) {
		return nil
	}

	err := networkCheckP2PLinkAlias(portName, portAlias)
	if err != nil {
		return fmt.Errorf("Refusing to remove interface %q: %w", portName, err)
	}

	if bridgeName != "" {
		_ = network.DetachInterface(bridgeName, portName)
	}

	err = network.InterfaceRemove(portName)
	if err != nil {
		return fmt.Errorf("Failed removing peer port %q: %w", portName, err)
	}

	return nil
}

// networkRemoveP2PPortsByAlias removes any port attached to a peer bridge that carries
// the expected ownership alias. It is used at start time to clean up a port left behind by
// a previous run whose name was not recorded.
func networkRemoveP2PPortsByAlias(bridgeName string, portAlias string) error {
	ports, err := networkBridgePorts(bridgeName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}

	for _, port := range ports {
		alias, err := networkGetP2PLinkAlias(port)
		if err != nil || alias != portAlias {
			continue
		}

		err = networkDetachP2PPort(bridgeName, port, portAlias)
		if err != nil {
			return err
		}
	}

	return nil
}

// networkP2PBridgeInUse reports whether a peer bridge still has a port that belongs to the
// same peer link. It is used to leave the bridge in place while the other endpoint is still
// attached.
func networkP2PBridgeInUse(bridgeName string, linkID string) (bool, error) {
	ports, err := networkBridgePorts(bridgeName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, err
	}

	aliasPrefix := nicP2PPortAliasPrefix + linkID + ":"
	for _, port := range ports {
		alias, err := networkGetP2PLinkAlias(port)
		if err != nil {
			continue
		}

		if strings.HasPrefix(alias, aliasPrefix) {
			return true, nil
		}
	}

	return false, nil
}

// networkRemoveP2PBridge removes a peer bridge and the ports it owns.
func networkRemoveP2PBridge(bridgeName string, bridgeAlias string, portAliasPrefix string) error {
	if bridgeName == "" || !network.InterfaceExists(bridgeName) {
		return nil
	}

	err := networkCheckP2PLinkAlias(bridgeName, bridgeAlias)
	if err != nil {
		return fmt.Errorf("Refusing to remove bridge %q: %w", bridgeName, err)
	}

	ports, err := networkBridgePorts(bridgeName)
	if err == nil {
		for _, port := range ports {
			alias, err := networkGetP2PLinkAlias(port)
			if err != nil || !strings.HasPrefix(alias, portAliasPrefix) {
				// Never delete a port that was not created by this peer link.
				continue
			}

			_ = network.InterfaceRemove(port)
		}
	}

	err = network.InterfaceRemove(bridgeName)
	if err != nil {
		return fmt.Errorf("Failed removing peer bridge %q: %w", bridgeName, err)
	}

	return nil
}

// networkBridgePorts returns the names of the ports attached to a Linux bridge.
func networkBridgePorts(bridgeName string) ([]string, error) {
	entries, err := os.ReadDir(fmt.Sprintf("/sys/class/net/%s/brif", bridgeName))
	if err != nil {
		return nil, err
	}

	ports := make([]string, 0, len(entries))
	for _, entry := range entries {
		ports = append(ports, entry.Name())
	}

	return ports, nil
}

// nicP2PCheckSameMember verifies that two peered instances are on the same cluster member.
// Empty locations are treated as unknown (for example on a non-clustered server).
func nicP2PCheckSameMember(localLocation string, peerLocation string) error {
	if localLocation == "" || peerLocation == "" {
		return nil
	}

	if localLocation != peerLocation {
		return fmt.Errorf("peer is on cluster member %q; both instances must be on the same cluster member %q", peerLocation, localLocation)
	}

	return nil
}
