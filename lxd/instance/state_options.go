package instance

import (
	"fmt"
	"strconv"
	"strings"
)

// StateField represents a specific state field that can be used with selective recursion.
type StateField string

const (
	// StateFieldDisk represents the disk usage state field.
	StateFieldDisk StateField = "state.disk"
	// StateFieldNetwork represents the network state field.
	StateFieldNetwork StateField = "state.network"
)

// StateRenderOptions controls which state fields should be included when rendering instance state.
type StateRenderOptions struct {
	IncludeDisk    bool
	IncludeNetwork bool
}

// DefaultStateRenderOptions returns the default state render options with all fields enabled.
func DefaultStateRenderOptions() StateRenderOptions {
	return StateRenderOptions{
		IncludeDisk:    true,
		IncludeNetwork: true,
	}
}

// ParseRecursionFields parses the recursion parameter with optional semicolon-separated fields,
// returning the recursion level and state render options.
// Format: "2" or "2;fields=state.disk" or "2;fields=state.disk,state.network"
// If no fields are specified, default options (all fields) are returned.
func ParseRecursionFields(recursionStr string, fields []string) (int, StateRenderOptions, error) {
	opts := DefaultStateRenderOptions()

	if recursionStr == "" {
		return 0, opts, nil
	}

	// Check if recursion string contains semicolon-separated fields
	var fieldsStr string
	usedSemicolonSyntax := strings.Contains(recursionStr, ";")

	if usedSemicolonSyntax {
		parts := strings.SplitN(recursionStr, ";", 2)
		recursionStr = parts[0]

		if len(parts) == 2 && strings.HasPrefix(parts[1], "fields=") {
			fieldsStr = strings.TrimPrefix(parts[1], "fields=")
		}
	}

	// Parse recursion level
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		return 0, opts, fmt.Errorf("Invalid recursion value: %q", recursionStr)
	}

	// Determine which fields to use
	var fieldsList []string

	if usedSemicolonSyntax {
		// Semicolon syntax was used
		if fieldsStr != "" {
			fieldsList = strings.Split(fieldsStr, ",")
		}
		// If fieldsStr is empty string (recursion=2;fields=), fieldsList remains nil/empty
	} else if len(fields) > 0 {
		// Separate fields parameter was used (backward compatibility)
		fieldsList = fields
	}

	// If no fields specified, return default options (all fields)
	if len(fieldsList) == 0 && !usedSemicolonSyntax {
		return recursion, opts, nil
	}

	opts = StateRenderOptions{
		IncludeDisk:    false,
		IncludeNetwork: false,
	}

	validFields := map[StateField]bool{
		StateFieldDisk:    true,
		StateFieldNetwork: true,
	}

	for _, field := range fieldsList {
		stateField := StateField(field)

		if !validFields[stateField] {
			return 0, opts, fmt.Errorf("Invalid state field: %q (valid fields: state.disk, state.network)", field)
		}

		switch stateField {
		case StateFieldDisk:
			opts.IncludeDisk = true
		case StateFieldNetwork:
			opts.IncludeNetwork = true
		}
	}

	return recursion, opts, nil
}
