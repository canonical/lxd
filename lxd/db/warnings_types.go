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
}

// WarningTypes associates a warning type to its type code.
var WarningTypes = map[string]WarningType{}

func init() {
	for code, name := range WarningTypeNames {
		WarningTypes[name] = code
	}
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
	}

	return WarningSeverityLow
}
