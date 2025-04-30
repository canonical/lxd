package shared

import (
	"fmt"
)

// Jmap is a simple wrapper around a map[string]any to provide some
// convenience methods for extracting values from the map.
type Jmap map[string]any

// GetString returns a string for the given key or an error.
func (m Jmap) GetString(key string) (string, error) {
	val, ok := m[key]
	if !ok {
		return "", fmt.Errorf("Response was missing %q", key)
	} else if val, ok := val.(string); !ok {
		return "", fmt.Errorf("%q was not a string", key)
	} else {
		return val, nil
	}
}

// GetMap returns a Jmap for the given key or an error.
func (m Jmap) GetMap(key string) (Jmap, error) {
	val, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("Response was missing %q", key)
	} else if val, ok := val.(map[string]any); !ok {
		return nil, fmt.Errorf("%q was not a map, got %T", key, m[key])
	} else {
		return val, nil
	}
}

// GetBool returns a bool for the given key or an error.
func (m Jmap) GetBool(key string) (bool, error) {
	val, ok := m[key]
	if !ok {
		return false, fmt.Errorf("Response was missing %q", key)
	} else if val, ok := val.(bool); !ok {
		return false, fmt.Errorf("%q was not a bool", key)
	} else {
		return val, nil
	}
}
