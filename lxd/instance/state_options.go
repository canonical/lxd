package instance

import (
	"fmt"
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

// ParseRecursionFields converts a fields slice into StateRenderOptions.
//
// A nil fields slice means no fields were specified (default behavior: all fields).
// An empty non-nil fields slice means no expensive fields should be fetched.
// A non-empty fields slice specifies exactly which fields to include.
func ParseRecursionFields(fields []string) (StateRenderOptions, error) {
	opts := DefaultStateRenderOptions()

	// If no fields specified, return default options (all fields).
	if fields == nil {
		return opts, nil
	}

	// Start with all fields disabled.
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
			return opts, fmt.Errorf("Invalid state field: %q (valid fields: state.disk, state.network)", field)
		}

		switch stateField {
		case StateFieldDisk:
			opts.IncludeDisk = true
		case StateFieldNetwork:
			opts.IncludeNetwork = true
		}
	}

	return opts, nil
}
