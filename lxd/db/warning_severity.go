//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// WarningSeverity represents the warning severity.
type WarningSeverity int

const (
	// WarningSeverityLow represents the low WarningSeverity
	WarningSeverityLow WarningSeverity = 1
	// WarningSeverityModerate represents the moderate WarningSeverity
	WarningSeverityModerate WarningSeverity = 2
	// WarningSeverityHigh represents the high WarningSeverity
	WarningSeverityHigh WarningSeverity = 3
)

// WarningSeverities associates a severity code to its name.
var WarningSeverities = map[WarningSeverity]string{
	WarningSeverityLow:      "low",
	WarningSeverityModerate: "moderate",
	WarningSeverityHigh:     "high",
}

// WarningSeverityTypes associates a warning severity to its type code.
var WarningSeverityTypes = map[string]WarningSeverity{}

func init() {
	for code, name := range WarningSeverities {
		WarningSeverityTypes[name] = code
	}
}
