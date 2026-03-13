//go:build linux && cgo && !agent

package warningtype

// Type is a numeric code indentifying the type of warning.
type Type int

const (
	// Undefined represents an undefined warning.
	Undefined Type = iota
	// MissingCGroupBlkio represents the missing CGroup blkio warning.
	MissingCGroupBlkio
	// MissingCGroupBlkioWeight represents the missing CGroup blkio.weight warning.
	MissingCGroupBlkioWeight
	// MissingCGroupCPUController represents the missing CGroup CPU controller warning.
	MissingCGroupCPUController
	// MissingCGroupCPUsetController represents the missing CGroup CPUset controller warning.
	MissingCGroupCPUsetController
	// MissingCGroupCPUacctController represents the missing CGroup CPUacct controller warning.
	MissingCGroupCPUacctController
	// MissingCGroupDevicesController represents the missing CGroup devices controller warning.
	MissingCGroupDevicesController
	// MissingCGroupFreezerController represents the missing CGroup freezer controller warning.
	MissingCGroupFreezerController
	// MissingCGroupHugetlbController represents the missing CGroup hugetlb controller warning.
	MissingCGroupHugetlbController
	// MissingCGroupMemoryController represents the missing CGroup memory controller warning.
	MissingCGroupMemoryController
	// MissingCGroupNetworkPriorityController represents the missing CGroup network priority controller warning.
	MissingCGroupNetworkPriorityController
	// MissingCGroupPidsController represents the missing CGroup pids controller warning.
	MissingCGroupPidsController
	// MissingCGroupMemorySwapAccounting represents the missing CGroup memory swap accounting warning.
	MissingCGroupMemorySwapAccounting
	// ClusterTimeSkew represents the cluster time skew warning.
	ClusterTimeSkew
	// AppArmorNotAvailable represents the AppArmor not available warning.
	AppArmorNotAvailable
	// MissingVirtiofsd represents the missing virtiofsd warning.
	MissingVirtiofsd
	// UnableToConnectToMAAS is deprecated and kept for backwards compatibility to preserve warning type codes.
	UnableToConnectToMAAS
	// AppArmorDisabledDueToRawDnsmasq represents the disabled AppArmor due to raw.dnsmasq warning.
	AppArmorDisabledDueToRawDnsmasq
	// LargerIPv6PrefixThanSupported represents the larger IPv6 prefix than supported warning.
	LargerIPv6PrefixThanSupported
	// ProxyBridgeNetfilterNotEnabled represents the proxy bridge netfilter not enable warning.
	ProxyBridgeNetfilterNotEnabled
	// NetworkUnvailable represents a network that cannot be initialized on the local server.
	NetworkUnvailable
	// OfflineClusterMember represents the offline cluster members warning.
	OfflineClusterMember
	// InstanceAutostartFailure represents the failure of instance autostart process after three retries.
	InstanceAutostartFailure
	// InstanceTypeNotOperational represents the lack of support for an instance driver.
	InstanceTypeNotOperational
	// StoragePoolUnvailable represents a storage pool that cannot be initialized on the local server.
	StoragePoolUnvailable
	// UnableToUpdateClusterCertificate represents the unable to update cluster certificate warning.
	UnableToUpdateClusterCertificate
)

// TypeNames associates a warning code to its name.
var TypeNames = map[Type]string{
	Undefined:                              "Undefined warning",
	MissingCGroupBlkio:                     "Could not find the CGroup blkio",
	MissingCGroupBlkioWeight:               "Could not find the CGroup blkio.weight",
	MissingCGroupCPUController:             "Could not find the CGroup CPU controller",
	MissingCGroupCPUsetController:          "Could not find the CGroup CPUset controller",
	MissingCGroupCPUacctController:         "Could not find the CGroup CPUacct controller",
	MissingCGroupDevicesController:         "Could not find the CGroup devices controller",
	MissingCGroupFreezerController:         "Could not find the CGroup freezer controller",
	MissingCGroupHugetlbController:         "Could not find the CGroup hugetlb controller",
	MissingCGroupMemoryController:          "Could not find the CGroup memory controller",
	MissingCGroupNetworkPriorityController: "Could not find the CGroup network priority controller",
	MissingCGroupPidsController:            "Could not find the CGroup pids controller",
	MissingCGroupMemorySwapAccounting:      "Could not find the CGroup memory swap accounting",
	ClusterTimeSkew:                        "Time skew detected between leader and local",
	AppArmorNotAvailable:                   "AppArmor support has been disabled",
	MissingVirtiofsd:                       "Missing virtiofsd",
	UnableToConnectToMAAS:                  "Cannot connect to MAAS",
	AppArmorDisabledDueToRawDnsmasq:        "Skipping AppArmor for dnsmasq due to raw.dnsmasq being set",
	LargerIPv6PrefixThanSupported:          "IPv6 networks with a prefix larger than 64 are not properly supported by dnsmasq",
	ProxyBridgeNetfilterNotEnabled:         "Proxy bridge netfilter not enabled",
	NetworkUnvailable:                      "Network unavailable",
	OfflineClusterMember:                   "Offline cluster member",
	InstanceAutostartFailure:               "Failed autostarting instance",
	InstanceTypeNotOperational:             "Instance type not operational",
	StoragePoolUnvailable:                  "Storage pool unavailable",
	UnableToUpdateClusterCertificate:       "Cannot update cluster certificate",
}

// Severity returns the severity of the warning type.
func (t Type) Severity() Severity {
	switch t {
	case Undefined:
		return SeverityLow
	case MissingCGroupBlkio:
		return SeverityLow
	case MissingCGroupBlkioWeight:
		return SeverityLow
	case MissingCGroupCPUController:
		return SeverityLow
	case MissingCGroupCPUsetController:
		return SeverityLow
	case MissingCGroupCPUacctController:
		return SeverityLow
	case MissingCGroupDevicesController:
		return SeverityLow
	case MissingCGroupFreezerController:
		return SeverityLow
	case MissingCGroupHugetlbController:
		return SeverityLow
	case MissingCGroupMemoryController:
		return SeverityLow
	case MissingCGroupNetworkPriorityController:
		return SeverityLow
	case MissingCGroupPidsController:
		return SeverityLow
	case MissingCGroupMemorySwapAccounting:
		return SeverityLow
	case ClusterTimeSkew:
		return SeverityLow
	case AppArmorNotAvailable:
		return SeverityLow
	case MissingVirtiofsd:
		return SeverityLow
	case UnableToConnectToMAAS:
		return SeverityLow
	case AppArmorDisabledDueToRawDnsmasq:
		return SeverityLow
	case LargerIPv6PrefixThanSupported:
		return SeverityLow
	case ProxyBridgeNetfilterNotEnabled:
		return SeverityLow
	case NetworkUnvailable:
		return SeverityHigh
	case OfflineClusterMember:
		return SeverityLow
	case InstanceAutostartFailure:
		return SeverityLow
	case InstanceTypeNotOperational:
		return SeverityLow
	case StoragePoolUnvailable:
		return SeverityHigh
	case UnableToUpdateClusterCertificate:
		return SeverityLow
	}

	return SeverityLow
}
