package device

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/device/nictype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/instance/operationlock"
	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/network"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
)

type nicP2P struct {
	deviceCommon
}

// CanHotPlug returns whether the device can be managed whilst the instance is running. Returns true.
func (d *nicP2P) CanHotPlug() bool {
	return true
}

// validateConfig checks the supplied config for correctness.
func (d *nicP2P) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	optionalFields := []string{
		"name",
		"mtu",
		"queue.tx.length",
		"hwaddr",
		"host_name",
		"limits.ingress",
		"limits.egress",
		"limits.max",
		"limits.priority",
		"ipv4.routes",
		"ipv6.routes",
		"boot.priority",
		"peer",
	}

	err := d.config.Validate(nicValidationRules([]string{}, optionalFields, instConf))
	if err != nil {
		return err
	}

	err = d.validatePeerChange(instConf)
	if err != nil {
		return err
	}

	if d.config["peer"] != "" {
		err = d.validatePeerConfig(instConf)
		if err != nil {
			return err
		}
	}

	return nil
}

// validatePeerChange ensures the peer relationship is only changed while both instances are stopped.
func (d *nicP2P) validatePeerChange(instConf instance.ConfigReader) error {
	if instConf.Name() == "" {
		return nil
	}

	// The temporary ConfigReader used for config validation has no ID. For a real
	// instance the config has already been validated before it is applied, so skip
	// the extra DB read here.
	if instConf.ID() != 0 {
		return nil
	}

	oldInst, err := instance.LoadByProjectAndName(d.state, instConf.Project().Name, instConf.Name())
	if err != nil {
		// The instance is being created.
		return nil
	}

	oldDev, oldDevExists := oldInst.LocalDevices()[d.name]
	if !oldDevExists {
		// The device is being added.
		return nil
	}

	oldPeerValue := oldDev["peer"]
	newPeerValue := d.config["peer"]
	if oldPeerValue == newPeerValue {
		return nil
	}

	// An instance that is starting can briefly report itself as stopped, so do not rely
	// only on the running state below. Serialize peer changes with start, restart and
	// stop operations so that a change cannot remove a bridge that a start just created.
	err = nicP2PCheckIdle(oldInst.Project().Name, oldInst.Name())
	if err != nil {
		return err
	}

	if oldInst.IsRunning() {
		return errors.New("The peer option cannot be changed while the instance is running; stop both instances first")
	}

	if oldPeerValue != "" {
		oldPeer, err := nicP2PParsePeer(oldPeerValue)
		if err == nil {
			oldPeerInst, err := instance.LoadByProjectAndName(d.state, instConf.Project().Name, oldPeer.Instance)
			if err == nil {
				err = nicP2PCheckIdle(oldPeerInst.Project().Name, oldPeerInst.Name())
				if err != nil {
					return err
				}

				if oldPeerInst.IsRunning() {
					return fmt.Errorf("The peer option cannot be changed while peer instance %q is running; stop both instances first", oldPeer.Instance)
				}
			}
		}
	}

	if newPeerValue != "" {
		newPeer, err := nicP2PParsePeer(newPeerValue)
		if err == nil {
			newPeerInst, err := instance.LoadByProjectAndName(d.state, instConf.Project().Name, newPeer.Instance)
			if err == nil {
				err = nicP2PCheckIdle(newPeerInst.Project().Name, newPeerInst.Name())
				if err != nil {
					return err
				}

				if newPeerInst.IsRunning() {
					return fmt.Errorf("The peer option cannot be changed to a device on running instance %q; stop both instances first", newPeer.Instance)
				}
			}
		}
	}

	return nil
}

// nicP2PCheckIdle returns an error if an instance has a start, restart, stop or
// migration operation in progress.
func nicP2PCheckIdle(projectName string, instanceName string) error {
	op := operationlock.Get(projectName, instanceName)
	if op == nil {
		return nil
	}

	if op.ActionMatch(operationlock.ActionStart, operationlock.ActionRestart, operationlock.ActionStop, operationlock.ActionRestore, operationlock.ActionMigrate) {
		return fmt.Errorf("Instance %q has a %s operation in progress; stop both instances first", instanceName, op.Action())
	}

	return nil
}

// validatePeerConfig validates the peer device config.
func (d *nicP2P) validatePeerConfig(instConf instance.ConfigReader) error {
	if instConf.Name() == "" {
		return errors.New("The peer option cannot be used in profiles")
	}

	// The host side of a peered NIC is a private bridge port whose name and configuration are
	// owned by LXD. User-controlled host-side names, routes, limits, MTU and tx queue length
	// are therefore not supported.
	for _, key := range []string{"host_name", "limits.ingress", "limits.egress", "limits.max", "limits.priority", "ipv4.routes", "ipv6.routes", "mtu", "queue.tx.length"} {
		if d.config[key] != "" {
			return fmt.Errorf("The peer option cannot be used with the %q option", key)
		}
	}

	peer, err := nicP2PParsePeer(d.config["peer"])
	if err != nil {
		return err
	}

	if peer.Instance == instConf.Name() {
		return errors.New("The peer device must be on a different instance")
	}

	// If the peer option is unchanged, do not validate the peer target again. Deleting a
	// peered instance is allowed and leaves the reference dangling, and the surviving
	// instance must still be able to update its config (for example to unset the peer
	// option). The reference is validated again at start time.
	if instConf.ID() == 0 {
		oldInst, err := instance.LoadByProjectAndName(d.state, instConf.Project().Name, instConf.Name())
		if err == nil {
			oldDev, ok := oldInst.LocalDevices()[d.name]
			if ok && oldDev["peer"] == d.config["peer"] {
				return nil
			}
		}
	}

	peerInst, err := instance.LoadByProjectAndName(d.state, instConf.Project().Name, peer.Instance)
	if err != nil {
		return fmt.Errorf("Failed loading peer instance %q: %w", peer.Instance, err)
	}

	if peerInst.IsSnapshot() {
		return fmt.Errorf("Peer instance %q cannot be a snapshot", peer.Instance)
	}

	err = nicP2PCheckSameMember(d.state.ServerName, peerInst.Location())
	if err != nil {
		return fmt.Errorf("Peer instance %q: %w", peer.Instance, err)
	}

	peerDev, peerDevLocal := peerInst.LocalDevices()[peer.Device]
	_, peerDevExpanded := peerInst.ExpandedDevices()[peer.Device]
	if !peerDevLocal {
		if peerDevExpanded {
			return fmt.Errorf("Peer device %q on instance %q must be defined locally on the instance, not inherited from a profile", peer.Device, peer.Instance)
		}

		// The peer device may not exist yet while the pair is being configured.
		return nil
	}

	return d.validatePeerDeviceConfig(instConf.Name(), peerInst.Project().Name, peer, peerDev)
}

// validatePeerDeviceConfig validates a peer instance's device against this device.
func (d *nicP2P) validatePeerDeviceConfig(localInstanceName string, peerProjectName string, peer *nicP2PPeer, peerDev deviceConfig.Device) error {
	peerNICType, err := nictype.NICType(d.state, peerProjectName, peerDev)
	if err != nil {
		return fmt.Errorf("Failed getting peer device %q NIC type: %w", peer.Device, err)
	}

	if peerNICType != "p2p" {
		return fmt.Errorf("Peer device %q on instance %q must be a p2p NIC", peer.Device, peer.Instance)
	}

	expectedPeer := localInstanceName + ":" + d.name
	peerBackRef := peerDev["peer"]
	if peerBackRef != "" && peerBackRef != expectedPeer {
		return fmt.Errorf("Peer device %q on instance %q is already paired with %q", peer.Device, peer.Instance, peerBackRef)
	}

	if d.config["hwaddr"] != "" && peerDev["hwaddr"] != "" && strings.EqualFold(d.config["hwaddr"], peerDev["hwaddr"]) {
		return fmt.Errorf("Peer device %q on instance %q must use a different MAC address", peer.Device, peer.Instance)
	}

	return nil
}

// validatePeerRuntime validates that the peer device exists and is compatible at start time.
func (d *nicP2P) validatePeerRuntime() error {
	if d.inst == nil {
		return errors.New("Peer validation requires an instance")
	}

	peer, err := nicP2PParsePeer(d.config["peer"])
	if err != nil {
		return err
	}

	peerInst, err := instance.LoadByProjectAndName(d.state, d.inst.Project().Name, peer.Instance)
	if err != nil {
		return fmt.Errorf("Failed loading peer instance %q: %w", peer.Instance, err)
	}

	if peerInst.IsSnapshot() {
		return fmt.Errorf("Peer instance %q cannot be a snapshot", peer.Instance)
	}

	err = nicP2PCheckSameMember(d.inst.Location(), peerInst.Location())
	if err != nil {
		return fmt.Errorf("Peer instance %q: %w", peer.Instance, err)
	}

	if d.inst.IsEphemeral() || peerInst.IsEphemeral() {
		return errors.New("Ephemeral instances cannot be peered")
	}

	peerDev, peerDevLocal := peerInst.LocalDevices()[peer.Device]
	_, peerDevExpanded := peerInst.ExpandedDevices()[peer.Device]
	if !peerDevLocal {
		if peerDevExpanded {
			return fmt.Errorf("Peer device %q on instance %q must be defined locally on the instance, not inherited from a profile", peer.Device, peer.Instance)
		}

		return fmt.Errorf("Peer device %q does not exist on instance %q", peer.Device, peer.Instance)
	}

	expectedPeer := d.inst.Name() + ":" + d.name
	if peerDev["peer"] != expectedPeer {
		return fmt.Errorf("Peer device %q on instance %q must reference %q", peer.Device, peer.Instance, expectedPeer)
	}

	return d.validatePeerDeviceConfig(d.inst.Name(), peerInst.Project().Name, peer, peerDev)
}

// PreStartCheck indicates if the device is available for starting.
func (d *nicP2P) PreStartCheck() error {
	if d.config["peer"] == "" {
		return nil
	}

	return d.validatePeerRuntime()
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicP2P) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return errors.New("Requires name property to start")
	}

	return nil
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicP2P) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicP2P)
	if !match {
		return []string{}
	}

	return []string{"limits.ingress", "limits.egress", "limits.max", "limits.priority", "ipv4.routes", "ipv6.routes"}
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicP2P) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	if d.config["peer"] != "" {
		return d.startPeer()
	}

	// A peer link was configured the last time this device started and its peer option
	// has since been unset. Remove the leftover peer host interfaces before starting as
	// a regular p2p NIC.
	if d.volatileGet()["peer.link_id"] != "" {
		err = d.removePeerLink()
		if err != nil {
			return nil, err
		}
	}

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)
	saveData["host_name"] = d.config["host_name"]

	var peerName string
	var mtu uint32

	// Create veth pair and configure the peer end with custom hwaddr and mtu if supplied.
	instType := d.inst.Type()
	switch instType {
	case instancetype.Container:
		if saveData["host_name"] == "" {
			saveData["host_name"], err = d.generateHostName("veth", d.config["hwaddr"])
			if err != nil {
				return nil, err
			}
		}

		peerName, mtu, err = networkCreateVethPair(saveData["host_name"], d.config)
	case instancetype.VM:
		if saveData["host_name"] == "" {
			saveData["host_name"], err = d.generateHostName("tap", d.config["hwaddr"])
			if err != nil {
				return nil, err
			}
		}

		peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
		mtu, err = networkCreateTap(saveData["host_name"], d.config, networkTapMultiQueueSupported(d.inst))
	}

	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = network.InterfaceRemove(saveData["host_name"]) })

	// Attempt to disable router advertisement acceptance.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, saveData)

	// Apply host-side routes to veth interface.
	err = networkNICRouteAdd(d.config["host_name"], append(shared.SplitNTrimSpace(d.config["ipv4.routes"], ",", -1, true), shared.SplitNTrimSpace(d.config["ipv6.routes"], ",", -1, true)...)...)
	if err != nil {
		return nil, err
	}

	// Apply host-side limits.
	err = networkSetupHostVethLimits(&d.deviceCommon, nil, false)
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
		{Key: "hwaddr", Value: d.config["hwaddr"]},
	}

	if instType == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "mtu", Value: strconv.FormatUint(uint64(mtu), 10)},
			}...)
	}

	revert.Success()
	return &runConf, nil
}

// startPeer starts a peered p2p NIC using a private host-inert bridge.
func (d *nicP2P) startPeer() (*deviceConfig.RunConfig, error) {
	peer, err := nicP2PParsePeer(d.config["peer"])
	if err != nil {
		return nil, err
	}

	if d.inst.ID() == 0 {
		return nil, errors.New("Cannot create a peer link for an instance without an ID")
	}

	lockName, err := nicP2PPeerLockName(d.inst.Project().Name, d.inst.Name(), d.name, d.config["peer"])
	if err != nil {
		return nil, err
	}

	unlock, err := locking.Lock(context.TODO(), lockName)
	if err != nil {
		return nil, err
	}

	defer unlock()

	// Resolve or assign the LXD-generated link ID for this device pair. It must be
	// done under the device-pair lock so two concurrent starts agree on one ID.
	linkID, err := d.peerLinkID(peer)
	if err != nil {
		return nil, err
	}

	names := nicP2PPeerNamesFor(d.inst.ID(), linkID)

	err = networkCreateP2PBridge(names.Bridge, names.BridgeAlias)
	if err != nil {
		return nil, err
	}

	// Drop everything delivered to the bridge's local port before any instance
	// port is attached. This also covers reserved link-local frames that are not
	// subject to the VLAN filter on the local port.
	err = d.state.Firewall.BridgeSetupLocalFilter(names.Bridge)
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	// Remove any stale port left by a previous run and verify that the interface we are
	// about to use is one LXD created. This also covers a crash between creating the port
	// and storing its name.
	err = networkRemoveP2PPortsByAlias(names.Bridge, names.PortAlias)
	if err != nil {
		return nil, err
	}

	// The port name is random and recorded in the device's volatile host_name, so that
	// stop and remove can find it again. It is never derived from user-controlled names.
	hostName, err := d.generateHostName(nicP2PPeerPortPrefix(d.inst.Type()), "")
	if err != nil {
		return nil, err
	}

	if hostName == "" {
		return nil, errors.New("Failed generating a host interface name for the peer link")
	}

	saveData := map[string]string{
		"peer.link_id": linkID,
		"host_name":    hostName,
	}

	// Strip user-controlled host-side settings even if validation was bypassed. The peer
	// link's host-side interfaces are always configured by LXD.
	portConfig := d.config.Clone()
	delete(portConfig, "mtu")
	delete(portConfig, "queue.tx.length")

	runConf := deviceConfig.RunConfig{}
	instType := d.inst.Type()
	switch instType {
	case instancetype.Container:
		instancePort, _, err := networkCreateVethPair(hostName, portConfig)
		if err != nil {
			return nil, err
		}

		revert.Add(func() { _ = network.InterfaceRemove(hostName) })

		err = networkSetP2PLinkAlias(hostName, names.PortAlias)
		if err != nil {
			return nil, err
		}

		runConf.NetworkInterface = []deviceConfig.RunConfigItem{
			{Key: "type", Value: "phys"},
			{Key: "name", Value: d.config["name"]},
			{Key: "flags", Value: "up"},
			{Key: "link", Value: instancePort},
			{Key: "hwaddr", Value: d.config["hwaddr"]},
		}

	case instancetype.VM:
		_, err := networkCreateTap(hostName, portConfig, networkTapMultiQueueSupported(d.inst))
		if err != nil {
			return nil, err
		}

		revert.Add(func() { _ = network.InterfaceRemove(hostName) })

		err = networkSetP2PLinkAlias(hostName, names.PortAlias)
		if err != nil {
			return nil, err
		}

		runConf.NetworkInterface = []deviceConfig.RunConfigItem{
			{Key: "type", Value: "phys"},
			{Key: "name", Value: d.config["name"]},
			{Key: "flags", Value: "up"},
			{Key: "link", Value: hostName},
			{Key: "hwaddr", Value: d.config["hwaddr"]},
			{Key: "devName", Value: d.name},
		}

	default:
		return nil, ErrUnsupportedDevType
	}

	err = networkAttachP2PPort(names.Bridge, hostName, names.PortAlias)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = networkDetachP2PPort(names.Bridge, hostName, names.PortAlias) })

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	revert.Success()
	return &runConf, nil
}

// peerLinkID resolves or creates the LXD-generated link ID shared by this device pair.
// It must be called with the device-pair lock held.
func (d *nicP2P) peerLinkID(peer *nicP2PPeer) (string, error) {
	// Reload the peer now that the lock is held; the other side may have written
	// its volatile link state while this start was waiting.
	peerInst, err := instance.LoadByProjectAndName(d.state, d.inst.Project().Name, peer.Instance)
	if err != nil {
		return "", fmt.Errorf("Failed loading peer instance %q: %w", peer.Instance, err)
	}

	peerVolatileKey := "volatile." + peer.Device + ".peer.link_id"
	peerLinkID := peerInst.LocalConfig()[peerVolatileKey]

	// Read the local state fresh as well: the other side may have published a link
	// ID while this start was waiting for the lock, and d.inst's cache can be stale.
	localInst, err := instance.LoadByProjectAndName(d.state, d.inst.Project().Name, d.inst.Name())
	if err != nil {
		return "", fmt.Errorf("Failed loading local instance %q: %w", d.inst.Name(), err)
	}

	localVolatileKey := "volatile." + d.name + ".peer.link_id"
	localLinkID := localInst.LocalConfig()[localVolatileKey]

	// Confirm that the devices still reference each other before publishing either ID.
	if localInst.LocalDevices()[d.name]["peer"] != d.config["peer"] || peerInst.LocalDevices()[peer.Device]["peer"] != localInst.Name()+":"+d.name {
		return "", errors.New("Peer devices no longer reference each other")
	}

	if localLinkID != "" && peerLinkID != "" && localLinkID != peerLinkID {
		return "", errors.New("Peer link state is inconsistent; remove and re-add both peer devices")
	}

	// A recreated endpoint has no ID. Reuse the survivor's ID in either start order.
	linkID := localLinkID
	if linkID == "" {
		linkID = peerLinkID
	}

	if linkID == "" {
		linkID = uuid.New().String()
	}

	// Publish the link ID to both devices while the lock is held. The peer is written
	// first so that a failure leaves this device without an ID and the start can simply be
	// retried. If the local write fails after the peer write succeeded, the next attempt
	// adopts the peer's ID, so a partial publication is always recoverable.
	err = peerInst.VolatileSet(map[string]string{
		peerVolatileKey: linkID,
	})
	if err != nil {
		return "", fmt.Errorf("Failed publishing peer link ID to instance %q: %w", peer.Instance, err)
	}

	err = d.volatileSet(map[string]string{
		"peer.link_id": linkID,
	})
	if err != nil {
		return "", err
	}

	return linkID, nil
}

// Update applies configuration changes to a device.
func (d *nicP2P) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	// Peered devices do not support in-place updates; peer changes are applied by removing
	// and re-adding the device.
	if d.config["peer"] != "" || oldDevices[d.name]["peer"] != "" {
		return nil
	}

	if !isRunning {
		return nil
	}

	err := d.validateEnvironment()
	if err != nil {
		return err
	}

	oldConfig := oldDevices[d.name]
	v := d.volatileGet()

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, v)
	networkVethFillFromVolatile(oldConfig, v)

	// Remove old host-side routes from veth interface.
	networkNICRouteDelete(oldConfig["host_name"], append(shared.SplitNTrimSpace(oldConfig["ipv4.routes"], ",", -1, true), shared.SplitNTrimSpace(oldConfig["ipv6.routes"], ",", -1, true)...)...)

	// Apply host-side routes to veth interface.
	err = networkNICRouteAdd(d.config["host_name"], append(shared.SplitNTrimSpace(d.config["ipv4.routes"], ",", -1, true), shared.SplitNTrimSpace(d.config["ipv6.routes"], ",", -1, true)...)...)
	if err != nil {
		return err
	}

	// Apply host-side limits.
	err = networkSetupHostVethLimits(&d.deviceCommon, oldConfig, false)
	if err != nil {
		return err
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicP2P) Stop() (*deviceConfig.RunConfig, error) {
	if d.config["peer"] != "" {
		return d.stopPeer()
	}

	// The peer option was unset after the device was started, so the device was started
	// as a peer and still owns peer host interfaces. The bridge is no longer shared, so
	// tear the whole link down instead of taking the regular p2p path.
	if d.volatileGet()["peer.link_id"] != "" {
		return &deviceConfig.RunConfig{
			PostHooks: []func() error{d.removePeerLink},
		}, nil
	}

	// Populate device config with volatile fields (hwaddr and host_name) if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

	err := networkClearHostVethLimits(&d.deviceCommon)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	return &runConf, nil
}

// stopPeer stops a peered p2p NIC. The private bridge is retained while the pair exists.
func (d *nicP2P) stopPeer() (*deviceConfig.RunConfig, error) {
	// Let the container/VM driver stop the instance side, then remove the host-side bridge
	// port in postStopPeer. Removing the host-side port also destroys the veth peer end, so
	// this works for normal stops, hot-removals and force-stops without relying on detach
	// callbacks that may not run after an abrupt stop.
	return &deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStopPeer},
	}, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicP2P) postStop() error {
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name": "",
		})
	}()

	v := d.volatileGet()

	networkVethFillFromVolatile(d.config, v)

	if d.config["host_name"] != "" && network.InterfaceExists(d.config["host_name"]) {
		// Removing host-side end of veth pair will delete the peer end too.
		err := network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			return fmt.Errorf("Failed removing interface %s: %w", d.config["host_name"], err)
		}
	}

	return nil
}

// postStopPeer is run after a peered p2p NIC is removed from the instance.
// The private bridge is retained so that the peer side can keep using it.
func (d *nicP2P) postStopPeer() error {
	if d.inst == nil {
		return nil
	}

	v := d.volatileGet()
	linkID := v["peer.link_id"]
	hostName := v["host_name"]

	// The port is gone, so its recorded name is no longer useful.
	defer func() {
		_ = d.volatileSet(map[string]string{
			"host_name": "",
		})
	}()

	if linkID == "" || hostName == "" {
		return nil
	}

	names := nicP2PPeerNamesFor(d.inst.ID(), linkID)

	unlock, err := d.peerLock()
	if err != nil {
		return err
	}

	defer unlock()

	return networkDetachP2PPort(names.Bridge, hostName, names.PortAlias)
}

// peerLock acquires the process-local lock for this device pair. It returns a no-op
// unlock function when the peer option is no longer set, because such a device can no
// longer race with a start of the same pair.
func (d *nicP2P) peerLock() (func(), error) {
	if d.inst == nil || d.config["peer"] == "" {
		return func() {}, nil
	}

	lockName, err := nicP2PPeerLockName(d.inst.Project().Name, d.inst.Name(), d.name, d.config["peer"])
	if err != nil {
		return nil, err
	}

	return locking.Lock(context.TODO(), lockName)
}

// PreRemoveCheck indicates if the device is available for removal.
func (d *nicP2P) PreRemoveCheck() error {
	if d.config["peer"] == "" || d.inst == nil {
		return nil
	}

	peer, err := nicP2PParsePeer(d.config["peer"])
	if err != nil {
		return err
	}

	peerInst, err := instance.LoadByProjectAndName(d.state, d.inst.Project().Name, peer.Instance)
	if err != nil {
		// The peer instance no longer exists, so there is nothing to keep the pair for.
		return nil
	}

	peerDev, ok := peerInst.LocalDevices()[peer.Device]
	if !ok {
		return nil
	}

	if peerDev["peer"] == d.inst.Name()+":"+d.name {
		return fmt.Errorf("Device is in use as the peer of %q on instance %q; unset that device's peer first", peer.Device, peer.Instance)
	}

	return nil
}

// Remove is run when the device is removed from the instance or the instance is deleted.
func (d *nicP2P) Remove() error {
	return d.removePeerLink()
}

// removePeerLink removes the host interfaces of a peer link and forgets its state.
// It is also used at start or stop time when the peer option was unset from a device
// that was already started, so that the host interfaces are not leaked.
func (d *nicP2P) removePeerLink() error {
	if d.inst == nil || d.inst.ID() == 0 {
		return nil
	}

	v := d.volatileGet()
	linkID := v["peer.link_id"]
	if linkID == "" {
		// No peer link state exists; nothing was created from this device.
		return nil
	}

	hostName := v["host_name"]
	names := nicP2PPeerNamesFor(d.inst.ID(), linkID)

	unlock, err := d.peerLock()
	if err != nil {
		return err
	}

	defer unlock()

	err = networkDetachP2PPort(names.Bridge, hostName, names.PortAlias)
	if err != nil {
		return err
	}

	// If the other endpoint is still attached, keep the bridge and its filter in place.
	// The peer's own removal cleans them up later. This allows this instance to be deleted
	// or have its peer option unset without disrupting a running peer.
	inUse, err := networkP2PBridgeInUse(names.Bridge, linkID)
	if err != nil {
		return err
	}

	if !inUse {
		err = networkRemoveP2PBridge(names.Bridge, names.BridgeAlias, nicP2PPortAliasPrefix+linkID+":")
		if err != nil {
			return err
		}

		err = d.state.Firewall.BridgeClearLocalFilter(names.Bridge)
		if err != nil {
			return err
		}
	}

	return d.volatileSet(map[string]string{
		"peer.link_id": "",
		"host_name":    "",
	})
}
