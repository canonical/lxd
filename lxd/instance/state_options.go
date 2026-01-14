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

// ParseRecursionFields parses the recursion parameter and optional fields parameter,
// returning the recursion level and state render options.
// If fieldsStr is nil, default options (all fields) are returned.
// If fieldsStr is non-nil but empty, no expensive fields are included.
func ParseRecursionFields(recursionStr string, fieldsStr *string) (int, StateRenderOptions, error) {
	opts := DefaultStateRenderOptions()

	if recursionStr == "" {
		return 0, opts, nil
	}

	// Parse recursion level
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		return 0, opts, fmt.Errorf("Invalid recursion value: %q", recursionStr)
	}

	// If no fields parameter provided, return default options (all fields)
	if fieldsStr == nil {
		return recursion, opts, nil
	}

	// Empty fields string means no expensive state fields
	if *fieldsStr == "" {
		return recursion, StateRenderOptions{
			IncludeDisk:    false,
			IncludeNetwork: false,
		}, nil
	}

	// Parse comma-separated fields
	fields := strings.Split(*fieldsStr, ",")

	opts = StateRenderOptions{
		IncludeDisk:    false,
		IncludeNetwork: false,
	}

	validFields := map[StateField]bool{
		StateFieldDisk:    true,
		StateFieldNetwork: true,
	}

	for _, field := range fields {
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
