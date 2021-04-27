//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// WarningStatus represents the warning status.
type WarningStatus int

const (
	// WarningStatusNew represents the New WarningStatus
	WarningStatusNew WarningStatus = 1
	// WarningStatusAcknowledged represents the Acknowledged WarningStatus
	WarningStatusAcknowledged WarningStatus = 2
	// WarningStatusResolved represents the Resolved WarningStatus
	WarningStatusResolved WarningStatus = 3
)

// WarningStatuses associates a warning code to its name.
var WarningStatuses = map[WarningStatus]string{
	WarningStatusNew:          "new",
	WarningStatusAcknowledged: "acknowledged",
	WarningStatusResolved:     "resolved",
}

// WarningStatusTypes associates a warning status to its type code.
var WarningStatusTypes = map[string]WarningStatus{}

func init() {
	for code, name := range WarningStatuses {
		WarningStatusTypes[name] = code
	}
}
