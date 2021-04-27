//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// WarningType is a numeric code indentifying the type of warning.
type WarningType int

const (
	// WarningUndefined represents an undefined warning
	WarningUndefined WarningType = iota
)

// WarningTypeNames associates a warning code to its name.
var WarningTypeNames = map[WarningType]string{
	WarningUndefined: "Undefined warning",
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
	}

	return WarningSeverityLow
}
