//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// WarningType is a numeric code indentifying the type of warning.
type WarningType int

const (
	// WarningUndefined represents an undefined warning
	WarningUndefined WarningType = iota
	// WarningMissingCGroupBlkio represents the missing CGroup blkio warning
	WarningMissingCGroupBlkio
	// WarningMissingCGroupBlkioWeight represents the missing CGroup blkio.weight warning
	WarningMissingCGroupBlkioWeight
	// WarningMissingCGroupCPUController represents the missing CGroup CPU controller warning
	WarningMissingCGroupCPUController
	// WarningMissingCGroupCPUsetController represents the missing GCgroup CPUset controller warning
	WarningMissingCGroupCPUsetController
	// WarningMissingCGroupCPUacctController represents the missing GCgroup CPUacct controller warning
	WarningMissingCGroupCPUacctController
	// WarningMissingCGroupDevicesController represents the missing GCgroup devices controller warning
	WarningMissingCGroupDevicesController
	// WarningMissingCGroupFreezerController represents the missing GCgroup freezer controller warning
	WarningMissingCGroupFreezerController
	// WarningMissingCGroupHugetlbController represents the missing GCgroup hugetlb controller warning
	WarningMissingCGroupHugetlbController
	// WarningMissingCGroupMemoryController represents the missing GCgroup memory controller warning
	WarningMissingCGroupMemoryController
	// WarningMissingCGroupNetworkPriorityController represents the missing GCgroup network priority controller warning
	WarningMissingCGroupNetworkPriorityController
	// WarningMissingCGroupPidsController represents the missing GCgroup pids controller warning
	WarningMissingCGroupPidsController
	// WarningMissingCGroupMemorySwapAccounting represents the missing GCgroup memory swap accounting warning
	WarningMissingCGroupMemorySwapAccounting
	// WarningClusterTimeSkew represents the cluster time skew warning
	WarningClusterTimeSkew
	// WarningAppArmorNotAvailable represents the AppArmor not available warning
	WarningAppArmorNotAvailable
	//WarningMissingVirtiofsd represents the missing virtiofsd warning
	WarningMissingVirtiofsd
	// WarningUnableToConnectToMAAS represents the unable to connect to MAAS warning
	WarningUnableToConnectToMAAS
	// WarningAppArmorDisabledDueToRawDnsmasq represents the disabled AppArmor due to raw.dnsmasq warning
	WarningAppArmorDisabledDueToRawDnsmasq
	// WarningLargerIPv6PrefixThanSupported represents the larger IPv6 prefix than supported warning
	WarningLargerIPv6PrefixThanSupported
	// WarningProxyBridgeNetfilterNotEnabled represents the proxy bridge netfilter not enable warning
	WarningProxyBridgeNetfilterNotEnabled
	// WarningNetworkStartupFailure represents the network startup failure warning
	WarningNetworkStartupFailure
	// WarningOfflineClusterMember represents the offline cluster members warning
	WarningOfflineClusterMember
	// WarningInstanceAutostartFailure represents the failure of instance autostart process after three retries
	WarningInstanceAutostartFailure
	//WarningInstanceTypeNotOperational represents the lack of support for an instance driver
	WarningInstanceTypeNotOperational
)

// WarningTypeNames associates a warning code to its name.
var WarningTypeNames = map[WarningType]string{
	WarningUndefined:                              "Undefined warning",
	WarningMissingCGroupBlkio:                     "Couldn't find the CGroup blkio",
	WarningMissingCGroupBlkioWeight:               "Couldn't find the CGroup blkio.weight",
	WarningMissingCGroupCPUController:             "Couldn't find the CGroup CPU controller",
	WarningMissingCGroupCPUsetController:          "Couldn't find the CGroup CPUset controller",
	WarningMissingCGroupCPUacctController:         "Couldn't find the CGroup CPUacct controller",
	WarningMissingCGroupDevicesController:         "Couldn't find the CGroup devices controller",
	WarningMissingCGroupFreezerController:         "Couldn't find the CGroup freezer controller",
	WarningMissingCGroupHugetlbController:         "Couldn't find the CGroup hugetlb controller",
	WarningMissingCGroupMemoryController:          "Couldn't find the CGroup memory controller",
	WarningMissingCGroupNetworkPriorityController: "Couldn't find the CGroup network priority controller",
	WarningMissingCGroupPidsController:            "Couldn't find the CGroup pids controller",
	WarningMissingCGroupMemorySwapAccounting:      "Couldn't find the CGroup memory swap accounting",
	WarningClusterTimeSkew:                        "Time skew detected between leader and local",
	WarningAppArmorNotAvailable:                   "AppArmor support has been disabled",
	WarningMissingVirtiofsd:                       "Missing virtiofsd",
	WarningUnableToConnectToMAAS:                  "Unable to connect to MAAS",
	WarningAppArmorDisabledDueToRawDnsmasq:        "Skipping AppArmor for dnsmasq due to raw.dnsmasq being set",
	WarningLargerIPv6PrefixThanSupported:          "IPv6 networks with a prefix larger than 64 aren't properly supported by dnsmasq",
	WarningProxyBridgeNetfilterNotEnabled:         "Proxy bridge netfilter not enabled",
	WarningNetworkStartupFailure:                  "Failed to start network",
	WarningOfflineClusterMember:                   "Offline cluster member",
	WarningInstanceAutostartFailure:               "Failed to autostart instance",
	WarningInstanceTypeNotOperational:             "Instance type not operational",
}

// Severity returns the severity of the warning type.
func (t WarningType) Severity() WarningSeverity {
	switch t {
	case WarningUndefined:
		return WarningSeverityLow
	case WarningMissingCGroupBlkio:
		return WarningSeverityLow
	case WarningMissingCGroupBlkioWeight:
		return WarningSeverityLow
	case WarningMissingCGroupCPUController:
		return WarningSeverityLow
	case WarningMissingCGroupCPUsetController:
		return WarningSeverityLow
	case WarningMissingCGroupCPUacctController:
		return WarningSeverityLow
	case WarningMissingCGroupDevicesController:
		return WarningSeverityLow
	case WarningMissingCGroupFreezerController:
		return WarningSeverityLow
	case WarningMissingCGroupHugetlbController:
		return WarningSeverityLow
	case WarningMissingCGroupMemoryController:
		return WarningSeverityLow
	case WarningMissingCGroupNetworkPriorityController:
		return WarningSeverityLow
	case WarningMissingCGroupPidsController:
		return WarningSeverityLow
	case WarningMissingCGroupMemorySwapAccounting:
		return WarningSeverityLow
	case WarningClusterTimeSkew:
		return WarningSeverityLow
	case WarningAppArmorNotAvailable:
		return WarningSeverityLow
	case WarningMissingVirtiofsd:
		return WarningSeverityLow
	case WarningUnableToConnectToMAAS:
		return WarningSeverityLow
	case WarningAppArmorDisabledDueToRawDnsmasq:
		return WarningSeverityLow
	case WarningLargerIPv6PrefixThanSupported:
		return WarningSeverityLow
	case WarningProxyBridgeNetfilterNotEnabled:
		return WarningSeverityLow
	case WarningNetworkStartupFailure:
		return WarningSeverityLow
	case WarningOfflineClusterMember:
		return WarningSeverityLow
	case WarningInstanceAutostartFailure:
		return WarningSeverityLow
	case WarningInstanceTypeNotOperational:
		return WarningSeverityLow
	}

	return WarningSeverityLow
}
