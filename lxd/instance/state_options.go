package instance

import (
	"fmt"
	"strconv"
	"strings"
)

// StateField represents a specific state field that can be selectively rendered.
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

// ParseRecursionFields parses the recursion parameter and returns the recursion level and state render options.
// It supports both traditional numeric recursion (e.g., "2") and selective field syntax (e.g., "[state.disk,state.network]").
func ParseRecursionFields(recursionStr string) (int, StateRenderOptions, error) {
	opts := DefaultStateRenderOptions()

	if recursionStr == "" {
		return 0, opts, nil
	}

	if strings.HasPrefix(recursionStr, "[") && strings.HasSuffix(recursionStr, "]") {
		fieldsStr := strings.TrimPrefix(recursionStr, "[")
		fieldsStr = strings.TrimSuffix(fieldsStr, "]")

		if fieldsStr == "" {
			return 2, StateRenderOptions{
				IncludeDisk:    false,
				IncludeNetwork: false,
			}, nil
		}

		fields := strings.Split(fieldsStr, ",")

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

		return 2, opts, nil
	}

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		return 0, opts, fmt.Errorf("Invalid recursion value: %q", recursionStr)
	}

	if recursion == 2 {
		return recursion, opts, nil
	}

	return recursion, opts, nil
}
