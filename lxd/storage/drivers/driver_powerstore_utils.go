package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/storage/connectors"
	"github.com/canonical/lxd/lxd/storage/drivers/powerstoreclient"
	"github.com/canonical/lxd/lxd/storage/drivers/tokencache"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
)

// powerStoreTokenCache stores shared PowerStore login sessions.
var powerStoreTokenCache = tokencache.New[powerstoreclient.LoginSession]("powerstore")

// newPowerStoreClient creates a new instance of the PowerStore HTTP API
// client.
func newPowerStoreClient(driver *powerstore) *powerstoreclient.Client {
	return &powerstoreclient.Client{
		Gateway:              driver.config["powerstore.gateway"],
		GatewaySkipTLSVerify: shared.IsFalse(driver.config["powerstore.gateway.verify"]),
		Username:             driver.config["powerstore.user.name"],
		Password:             driver.config["powerstore.user.password"],
		TokenCache:           powerStoreTokenCache,
		VolumeNamePrefix:     driver.volumeResourceNamePrefix(),
		HostNamePrefix:       powerStoreResourceNamePrefix,
	}
}

// powerStoreTarget represent a PowerStore connection target.
type powerStoreTarget struct {
	Address       string
	QualifiedName string
}

// powerStoreGroupTargetsAddressesByQualifiedName combines target addresses
// from targets with the same qualified names together into a single map key.
func powerStoreGroupTargetsAddressesByQualifiedName(targets ...powerStoreTarget) map[string][]string {
	grouped := map[string][]string{}
	// Attempt to preserve order while grouping.
	for _, target := range targets {
		grouped[target.QualifiedName] = append(grouped[target.QualifiedName], target.Address)
	}

	for qn, addresses := range grouped {
		grouped[qn] = shared.Unique(addresses)
	}

	return grouped
}

// client returns the PowerStore API client.
// A new client gets created if one does not exists.
func (d *powerstore) client() *powerstoreclient.Client {
	if d.apiClient == nil {
		d.apiClient = newPowerStoreClient(d)
	}

	return d.apiClient
}

// connector retrieves an initialized storage connector based on the configured
// PowerStore mode. The connector is cached in the driver struct.
func (d *powerstore) connector() (connectors.Connector, error) {
	if d.storageConnector == nil {
		mt, err := powerStoreSupportedModesAndTransports.Find(d.config["powerstore.mode"], d.config["powerstore.transport"])
		if err != nil {
			return nil, err
		}

		connector, err := connectors.NewConnector(mt.ConnectorType, d.state.OS.ServerUUID)
		if err != nil {
			return nil, err
		}

		d.storageConnector = connector
	}

	return d.storageConnector, nil
}

// targets return discovered PowerStore targets (their addresses and associated
// qualified names).
func (d *powerstore) targets() ([]powerStoreTarget, error) {
	if len(d.discoveredTargets) == 0 {
		connector, err := d.connector()
		if err != nil {
			return nil, err
		}

		discoveryAddresses := shared.SplitNTrimSpace(d.config["powerstore.discovery"], ",", -1, true)
		var discoveryLogRecords []any
		for _, addr := range discoveryAddresses {
			discovered, err := connector.Discover(d.state.ShutdownCtx, addr)
			if err != nil {
				// Underlying connector should log a waring.
				continue
			}

			discoveryLogRecords = append(discoveryLogRecords, discovered...)
		}

		if len(discoveryLogRecords) == 0 {
			return nil, errors.New("Failed fetching a discovery log record from any of the target addresses")
		}

		discoveredTargets := []powerStoreTarget{}
		userForcedTargetAddresses := shared.SplitNTrimSpace(d.config["powerstore.target"], ",", -1, true)
		parser := d.discoveryLogRecordParser(userForcedTargetAddresses)
		for _, record := range discoveryLogRecords {
			target, includeTarget, err := parser(record)
			if err != nil {
				return nil, err
			}

			if !includeTarget {
				continue
			}

			discoveredTargets = append(discoveredTargets, target)
		}

		discoveredTargets = shared.Unique(discoveredTargets)

		if len(discoveredTargets) == 0 {
			return nil, errors.New("Failed fetching a discovery log record from any of the discovery addresses")
		}

		d.discoveredTargets = discoveredTargets
	}

	return d.discoveredTargets, nil
}

// discoveryLogRecordParser returns a parsing function that converts single
// discovery log entry to target.
func (d *powerstore) discoveryLogRecordParser(filterTargetAddresses []string) func(any) (powerStoreTarget, bool, error) {
	mode := d.config["powerstore.mode"]
	transport := d.config["powerstore.transport"]
	switch {
	case mode == powerStoreModeISCSI && transport == powerStoreTransportTCP:
		filterTargetAddresses = slices.Clone(filterTargetAddresses)
		for i := range filterTargetAddresses {
			filterTargetAddresses[i] = shared.EnsurePort(filterTargetAddresses[i], connectors.ISCSIDefaultPort)
		}

		return func(record any) (powerStoreTarget, bool, error) {
			r, ok := record.(connectors.ISCSIDiscoveryLogRecord)
			if !ok {
				return powerStoreTarget{}, false, fmt.Errorf("Invalid discovery log record entry type %T is not connectors.ISCSIDiscoveryLogRecord", record)
			}

			target := powerStoreTarget{
				Address:       r.Address,
				QualifiedName: r.IQN,
			}

			if len(filterTargetAddresses) > 0 && !slices.Contains(filterTargetAddresses, target.Address) {
				return powerStoreTarget{}, false, nil
			}

			return target, true, nil
		}

	case mode == powerStoreModeNVME && transport == powerStoreTransportTCP:
		filterTargetAddresses = slices.Clone(filterTargetAddresses)
		for i := range filterTargetAddresses {
			filterTargetAddresses[i] = shared.EnsurePort(filterTargetAddresses[i], connectors.NVMeDefaultTransportPort)
		}

		return func(record any) (powerStoreTarget, bool, error) {
			r, ok := record.(connectors.NVMeDiscoveryLogRecord)
			if !ok {
				return powerStoreTarget{}, false, fmt.Errorf("Invalid discovery log record entry type %T is not connectors.NVMeDiscoveryLogRecord", record)
			}

			target := powerStoreTarget{
				Address:       net.JoinHostPort(r.TransportAddress, r.TransportServiceIdentifier),
				QualifiedName: r.SubNQN,
			}

			if len(filterTargetAddresses) > 0 && !slices.Contains(filterTargetAddresses, target.Address) {
				return powerStoreTarget{}, false, nil
			}

			return target, true, nil
		}
	}

	panic(fmt.Errorf("storage: powerstore: bad configuration (mode: %q, transport: %q); this case should never be reached", mode, transport))
}

// powerStoreResourceNamePrefix common prefix for all resource names in PowerStore.
const powerStoreResourceNamePrefix = "lxd:"

const (
	powerStoreVolPrefixSep       = "_" // volume name prefix separator
	powerStoreContainerVolPrefix = "c" // volume name prefix indicating container volume
	powerStoreVMVolPrefix        = "v" // volume name prefix indicating virtual machine volume
	powerStoreImageVolPrefix     = "i" // volume name prefix indicating image volume
	powerStoreCustomVolPrefix    = "u" // volume name prefix indicating custom volume
)

// powerStoreVolTypePrefixes maps volume type to storage volume name prefix.
var powerStoreVolTypePrefixes = map[VolumeType]string{
	VolumeTypeContainer: powerStoreContainerVolPrefix,
	VolumeTypeVM:        powerStoreVMVolPrefix,
	VolumeTypeImage:     powerStoreImageVolPrefix,
	VolumeTypeCustom:    powerStoreCustomVolPrefix,
}

// powerStoreVolTypePrefixesRev maps storage volume name prefix to volume type.
var powerStoreVolTypePrefixesRev = map[string]VolumeType{
	powerStoreContainerVolPrefix: VolumeTypeContainer,
	powerStoreVMVolPrefix:        VolumeTypeVM,
	powerStoreImageVolPrefix:     VolumeTypeImage,
	powerStoreCustomVolPrefix:    VolumeTypeCustom,
}

const (
	powerStoreVolSuffixSep   = "." // volume name suffix separator
	powerStoreBlockVolSuffix = "b" // volume name suffix used for block content type volumes
	powerStoreISOVolSuffix   = "i" // volume name suffix used for iso content type volumes
)

// powerStoreVolContentTypeSuffixes maps volume content type to storage volume name suffix.
var powerStoreVolContentTypeSuffixes = map[ContentType]string{
	ContentTypeBlock: powerStoreBlockVolSuffix,
	ContentTypeISO:   powerStoreISOVolSuffix,
}

// powerStoreVolContentTypeSuffixesRev maps storage volume name suffix to volume content type.
var powerStoreVolContentTypeSuffixesRev = map[string]ContentType{
	powerStoreBlockVolSuffix: ContentTypeBlock,
	powerStoreISOVolSuffix:   ContentTypeISO,
}

// powerStorePoolAndVolSep separates pool name and volume data in encoded volume names.
const powerStorePoolAndVolSep = "-"

// volumeResourceNamePrefix returns the prefix used by all volume resource
// names in PowerStore associated with the current storage pool.
func (d *powerstore) volumeResourceNamePrefix() string {
	poolHash := sha256.Sum256([]byte(d.Name()))
	poolName := base64.StdEncoding.EncodeToString(poolHash[:])
	return powerStoreResourceNamePrefix + poolName + powerStorePoolAndVolSep
}

// volumeResourceName derives the name of a volume resource in PowerStore from
// the provided volume.
func (d *powerstore) volumeResourceName(vol Volume) (string, error) {
	volUUID, err := uuid.Parse(vol.config["volatile.uuid"])
	if err != nil {
		return "", fmt.Errorf(`Failed parsing "volatile.uuid" from volume %q: %w`, vol.name, err)
	}

	volName := base64.StdEncoding.EncodeToString(volUUID[:])

	// Search for the volume type prefix, and if found, prepend it to the volume
	// name.
	prefix := powerStoreVolTypePrefixes[vol.volType]
	if prefix != "" {
		volName = prefix + powerStoreVolPrefixSep + volName
	}

	// Search for the content type suffix, and if found, append it to the volume
	// name.
	suffix := powerStoreVolContentTypeSuffixes[vol.contentType]
	if suffix != "" {
		volName = volName + powerStoreVolSuffixSep + suffix
	}

	return d.volumeResourceNamePrefix() + volName, nil
}

// extractDataFromVolumeResourceName decodes the PowerStore volume resource
// name and extracts the stored data.
func (d *powerstore) extractDataFromVolumeResourceName(name string) (poolHash string, volType VolumeType, volUUID uuid.UUID, volContentType ContentType, err error) {
	prefixLess, hasPrefix := strings.CutPrefix(name, powerStoreResourceNamePrefix)
	if !hasPrefix {
		return "", "", uuid.Nil, "", fmt.Errorf("Cannot decode volume name %q: invalid name format", name)
	}

	poolHash, volName, ok := strings.Cut(prefixLess, powerStorePoolAndVolSep)
	if !ok || poolHash == "" || volName == "" {
		return "", "", uuid.Nil, "", fmt.Errorf("Cannot decode volume name %q: invalid name format", name)
	}

	prefix, volNameWithoutPrefix, ok := strings.Cut(volName, powerStoreVolPrefixSep)
	if ok {
		volName = volNameWithoutPrefix
		volType = powerStoreVolTypePrefixesRev[prefix]
	}

	volNameWithoutSuffix, suffix, ok := strings.Cut(volName, powerStoreVolSuffixSep)
	if ok {
		volName = volNameWithoutSuffix
		volContentType = powerStoreVolContentTypeSuffixesRev[suffix]
	}

	binUUID, err := base64.StdEncoding.DecodeString(volName)
	if err != nil {
		return poolHash, volType, volUUID, volContentType, fmt.Errorf("Cannot decode volume name %q: %w", name, err)
	}

	volUUID, err = uuid.FromBytes(binUUID)
	if err != nil {
		return poolHash, volType, volUUID, volContentType, fmt.Errorf("Failed parsing UUID from decoded volume name: %w", err)
	}

	return poolHash, volType, volUUID, volContentType, nil
}

// volumeWWN derives the world wide name of a volume resource in PowerStore
// from the provided volume resource.
func (d *powerstore) volumeWWN(volResource *powerstoreclient.VolumeResource) string {
	_, wwn, _ := strings.Cut(volResource.WWN, ".")
	return wwn
}

// hostResourceName derives the name of a host resource in PowerStore
// associated with the current node or host and mode. On success, it returns
// name applicable to use as PowerStore host resource name along of full
// unencoded name.
func (d *powerstore) hostResourceName() (resource string, hostname string, err error) {
	hostname, err = ResolveServerName(d.state.ServerName)
	if err != nil {
		return "", "", err
	}

	resource = fmt.Sprintf("%s%s-%s/%s", powerStoreResourceNamePrefix, hostname, d.config["powerstore.mode"], d.config["powerstore.transport"])
	if len(resource) > 128 { // PowerStore limits host resource name to 128 characters
		hostnameHash := sha256.Sum256([]byte(hostname))
		resource = fmt.Sprintf("%s%s-%s/%s", powerStoreResourceNamePrefix, base64.StdEncoding.EncodeToString(hostnameHash[:]), d.config["powerstore.mode"], d.config["powerstore.transport"])
	}

	return resource, hostname, nil
}

// initiator returns PowerStore initiator resource associated the current host,
// mode and transport.
func (d *powerstore) initiator() (*powerstoreclient.HostInitiatorResource, error) {
	if d.initiatorResource == nil {
		initiatorResource := &powerstoreclient.HostInitiatorResource{}
		connector, err := d.connector()
		if err != nil {
			return nil, err
		}

		initiatorResource.PortName, err = connector.QualifiedName()
		if err != nil {
			return nil, err
		}

		switch {
		case d.config["powerstore.mode"] == connectors.TypeNVME:
			// PowerStore uses the same port type for both NVMe/TCP and NVMe/FC
			initiatorResource.PortType = powerstoreclient.InitiatorPortTypeEnumNVMe
		case d.config["powerstore.mode"] == connectors.TypeISCSI && d.config["powerstore.transport"] == "tcp":
			initiatorResource.PortType = powerstoreclient.InitiatorPortTypeEnumISCSI
		case d.config["powerstore.mode"] == connectors.TypeISCSI && d.config["powerstore.transport"] == "fc":
			initiatorResource.PortType = powerstoreclient.InitiatorPortTypeEnumFC
		default:
			return nil, fmt.Errorf("Cannot determine PowerStore initiator port type (mode: %q, transport: %q)", d.config["powerstore.mode"], d.config["powerstore.transport"])
		}

		d.initiatorResource = initiatorResource
	}

	return d.initiatorResource, nil
}

// getMappedDevicePathByVolumeID returns the local device path associated with
// the given PowerStore volume WWN.
func (d *powerstore) getMappedDevicePathByVolumeWWN(volWWN string, wait bool) (devicePath string, err error) {
	connector, err := d.connector()
	if err != nil {
		return "", err
	}

	devicePathFilter := func(path string) bool {
		return strings.Contains(path, volWWN)
	}

	if wait {
		// Wait for the device path to appear as the volume has been just mapped to the host.
		devicePath, err = connector.WaitDiskDevicePath(d.state.ShutdownCtx, devicePathFilter)
	} else {
		// Get the the device path without waiting.
		devicePath, err = connector.GetDiskDevicePath(devicePathFilter)
	}

	if err != nil {
		return "", err
	}

	return devicePath, nil
}

// mapVolumeByVolumeResource maps the volume associated with the given
// PowerStore volume resource onto this host.
func (d *powerstore) mapVolumeByVolumeResource(volResource *powerstoreclient.VolumeResource) (revert.Hook, error) {
	connector, err := d.connector()
	if err != nil {
		return nil, err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return nil, err
	}

	defer unlock()

	reverter := revert.New()
	defer reverter.Fail()

	hostResource, err := d.getOrCreateHostWithInitiatorResource()
	if err != nil {
		return nil, err
	}

	reverter.Add(func() { _ = d.deleteHostAndInitiatorResource(hostResource) })

	mapped := slices.ContainsFunc(volResource.MappedVolumes, func(mappingResource *powerstoreclient.HostVolumeMappingResource) bool {
		return mappingResource.HostID == hostResource.ID
	})
	if !mapped {
		err := d.client().AttachHostToVolume(d.state.ShutdownCtx, hostResource.ID, volResource.ID)
		if err != nil {
			return nil, err
		}

		reverter.Add(func() { _ = d.client().DetachHostFromVolume(d.state.ShutdownCtx, hostResource.ID, volResource.ID) })
	}

	// Reverting mapping or connection outside mapVolume function
	// could conflict with other ongoing operations as lock will
	// already be released. Therefore, use unmapVolume instead
	// because it ensures the lock is acquired and accounts for
	// an existing session before unmapping a volume.
	outerReverter := revert.New()
	if !mapped {
		outerReverter.Add(func() { _ = d.unmapVolumeByVolumeResource(volResource) })
	}

	targets, err := d.targets()
	if err != nil {
		return nil, err
	}

	for qualifiedName, addresses := range powerStoreGroupTargetsAddressesByQualifiedName(targets...) {
		cleanup, err := connector.Connect(d.state.ShutdownCtx, qualifiedName, addresses...)
		if err != nil {
			return nil, err
		}

		reverter.Add(cleanup)
		outerReverter.Add(cleanup)
	}

	reverter.Success()
	return outerReverter.Fail, nil
}

// unmapVolumeByVolumeResource unmaps the volume associated with the given
// PowerStore volume resource from this host.
func (d *powerstore) unmapVolumeByVolumeResource(volResource *powerstoreclient.VolumeResource) error {
	connector, err := d.connector()
	if err != nil {
		return err
	}

	unlock, err := remoteVolumeMapLock(connector.Type(), d.Info().Name)
	if err != nil {
		return err
	}

	defer unlock()

	hostResource, _, err := d.getHostWithInitiatorResource()
	if err != nil {
		return err
	}

	var volumePath string
	if volResource != nil {
		// Get a path of a block device we want to unmap, ignoring any errors.
		volumePath, _ := d.getMappedDevicePathByVolumeWWN(d.volumeWWN(volResource), false)
		err = connector.RemoveDiskDevice(d.state.ShutdownCtx, volumePath)
		if err != nil {
			return fmt.Errorf("Cannot unmap device for PowerStore volume resource with ID %q: %w", volResource.ID, err)
		}
	}

	if hostResource != nil && volResource != nil {
		for _, mappingResource := range volResource.MappedVolumes {
			if mappingResource.HostID != hostResource.ID {
				continue
			}

			err := d.client().DetachHostFromVolume(d.state.ShutdownCtx, mappingResource.HostID, volResource.ID)
			if err != nil {
				return err
			}
		}
	}

	if volumePath != "" {
		// Wait until the volume has disappeared.
		ctx, cancel := context.WithTimeout(d.state.ShutdownCtx, 30*time.Second)
		defer cancel()

		if !block.WaitDiskDeviceGone(ctx, volumePath) {
			return fmt.Errorf("Timeout exceeded waiting for disk device %q related to PowerStore volume resource with ID %q to disappear", volumePath, volResource.ID)
		}
	}

	// Disconnect connector if:
	// - there is no associated PowerStore host resource,
	// - there are no other volumes mapped.
	if hostResource == nil || len(hostResource.MappedHosts) == 0 {
		targets, err := d.targets()
		if err != nil {
			return err
		}

		for qualifiedName := range powerStoreGroupTargetsAddressesByQualifiedName(targets...) {
			err = connector.Disconnect(qualifiedName)
			if err != nil {
				return err
			}
		}
	}

	if hostResource != nil {
		err = d.deleteHostAndInitiatorResource(hostResource)
		if err != nil {
			return err
		}
	}

	return nil
}

// getVolumeResourceByVolume retrieves volume resource associated with
// the provided volume.
func (d *powerstore) getVolumeResourceByVolume(vol Volume) (*powerstoreclient.VolumeResource, error) {
	volResourceName, err := d.volumeResourceName(vol)
	if err != nil {
		return nil, err
	}

	volResource, err := d.client().GetVolumeByName(d.state.ShutdownCtx, volResourceName)
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving PowerStore volume resource %q associated with volume %q: %w", volResourceName, vol.name, err)
	}

	return volResource, nil
}

// getExistingVolumeResourceByVolume retrieves volume resource associated with
// the provided volume, just like getVolumeResourceByVolume function, but
// returns error if the volume resource does not exists.
func (d *powerstore) getExistingVolumeResourceByVolume(vol Volume) (*powerstoreclient.VolumeResource, error) {
	volResourceName, err := d.volumeResourceName(vol)
	if err != nil {
		return nil, err
	}

	volResource, err := d.client().GetVolumeByName(d.state.ShutdownCtx, volResourceName)
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving PowerStore volume resource %q associated with volume %q: %w", volResourceName, vol.name, err)
	}

	if volResource == nil {
		return nil, fmt.Errorf("Failed retrieving PowerStore volume resource %q associated with volume %q: resource not found", volResourceName, vol.name)
	}

	return volResource, nil
}

// createVolumeResource creates volume resources in PowerStore associated with
// the provided volume.
func (d *powerstore) createVolumeResource(vol Volume) (*powerstoreclient.VolumeResource, error) {
	sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return nil, err
	}

	if sizeBytes < powerStoreMinVolumeSizeBytes {
		return nil, fmt.Errorf("Volume size is too small, supported minimum %s", powerStoreMinVolumeSizeUnit)
	}

	if sizeBytes > powerStoreMaxVolumeSizeBytes {
		return nil, fmt.Errorf("Volume size is too large, supported maximum %s", powerStoreMaxVolumeSizeUnit)
	}

	volResourceName, err := d.volumeResourceName(vol)
	if err != nil {
		return nil, err
	}

	typ := "lxd"
	if vol.volType != "" {
		typ += ":" + string(vol.volType)
	}

	if vol.contentType != "" {
		typ += ":" + string(vol.contentType)
	}

	volResource := &powerstoreclient.VolumeResource{
		Name:         volResourceName,
		Description:  limitString("LXD Name: "+vol.name, 128), // maximum allowed value length for volume description field is 128
		Size:         sizeBytes,
		AppType:      "Other",
		AppTypeOther: limitString(typ, 32), // maximum allowed value length for volume app_type_other field is 32,
	}

	err = d.client().CreateVolume(d.state.ShutdownCtx, volResource)
	if err != nil {
		return nil, err
	}

	return volResource, nil
}

// deleteVolumeResource deletes volume resources in PowerStore.
func (d *powerstore) deleteVolumeResource(volResource *powerstoreclient.VolumeResource) error {
	for _, mappingResource := range volResource.MappedVolumes {
		err := d.client().DetachHostFromVolume(d.state.ShutdownCtx, mappingResource.HostID, volResource.ID)
		if err != nil {
			return err
		}
	}
	for _, volumeGroupResource := range volResource.VolumeGroups {
		err := d.client().RemoveMembersFromVolumeGroup(d.state.ShutdownCtx, volumeGroupResource.ID, []string{volResource.ID})
		if err != nil {
			return err
		}
	}
	return d.client().DeleteVolumeByID(d.state.ShutdownCtx, volResource.ID)
}

// getHostWithInitiatorResource retrieves initiator and associated host
// resources from PowerStore associated with the current host, mode and
// transport.
func (d *powerstore) getHostWithInitiatorResource() (*powerstoreclient.HostResource, *powerstoreclient.HostInitiatorResource, error) {
	initiatorResource, err := d.initiator()
	if err != nil {
		return nil, nil, err
	}

	hostResource, err := d.client().GetHostByInitiator(d.state.ShutdownCtx, initiatorResource)
	if err != nil {
		return nil, nil, err
	}

	if hostResource != nil {
		// host with initiator already exists
		return hostResource, initiatorResource, nil
	}

	// no initiator found
	hostResourceName, _, err := d.hostResourceName()
	if err != nil {
		return nil, nil, err
	}

	hostResource, err = d.client().GetHostByName(d.state.ShutdownCtx, hostResourceName)
	if err != nil {
		return nil, nil, err
	}

	if hostResource != nil {
		// host without initiator found
		return hostResource, nil, nil
	}

	// no host or initiator exists
	return nil, nil, nil
}

// getOrCreateHostWithInitiatorResource retrieves (or creates if missing)
// initiator and associated host resources in PowerStore associated with
// the current host, mode and transport.
func (d *powerstore) getOrCreateHostWithInitiatorResource() (*powerstoreclient.HostResource, error) {
	hostResource, initiatorResource, err := d.getHostWithInitiatorResource()
	if err != nil {
		return nil, err
	}

	if hostResource == nil {
		// no host or initiator exists
		initiatorResource, err = d.initiator()
		if err != nil {
			return nil, err
		}

		hostResourceName, hostname, err := d.hostResourceName()
		if err != nil {
			return nil, err
		}

		hostResource = &powerstoreclient.HostResource{
			Name:        hostResourceName,
			Description: limitString("LXD Name: "+hostname, 256), // maximum allowed value length for host description field is 256
			OsType:      powerstoreclient.OSTypeEnumLinux,
			Initiators:  []*powerstoreclient.HostInitiatorResource{initiatorResource},
		}

		err = d.client().CreateHost(d.state.ShutdownCtx, hostResource)
		if err != nil {
			return nil, err
		}

		return hostResource, nil
	}

	if initiatorResource == nil {
		// host exists but initiator is missing
		initiatorResource, err = d.initiator()
		if err != nil {
			return nil, err
		}

		err = d.client().AddInitiatorToHostByID(d.state.ShutdownCtx, hostResource.ID, initiatorResource)
		if err != nil {
			return nil, err
		}

		return d.client().GetHostByID(d.state.ShutdownCtx, hostResource.ID) // refetch to refresh the data
	}

	// host with initiator already exists
	return hostResource, nil
}

// deleteHostAndInitiatorResource deletes initiator and associated host
// resources in PowerStore if there are no mapped (attached) volumes.
func (d *powerstore) deleteHostAndInitiatorResource(hostResource *powerstoreclient.HostResource) error {
	initiatorResource, err := d.initiator()
	if err != nil {
		return err
	}

	hostResourceName, _, err := d.hostResourceName()
	if err != nil {
		return err
	}

	if len(hostResource.MappedHosts) > 0 {
		// host has some other volumes mapped
		return nil
	}

	if len(hostResource.Initiators) > 1 {
		// host has multiple initiators associated
		return nil
	}

	if len(hostResource.Initiators) == 1 && (hostResource.Initiators[0].PortName != initiatorResource.PortName || hostResource.Initiators[0].PortType != initiatorResource.PortType) {
		// associated initiator do not matches the expected one
		return nil
	}

	if hostResource.Name != hostResourceName {
		// host is not managed by LXD
		return nil
	}

	return d.client().DeleteHostByID(d.state.ShutdownCtx, hostResource.ID)
}
