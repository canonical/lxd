package config

import (
	"maps"
)

// deviceEquals checks for any difference and addition/removal of properties.
func deviceEquals(old Device, d Device) bool {
	return maps.Equal(old, d)
}

// deviceEqualsDiffKeys checks for any difference and addition/removal of properties and returns a list of changes.
func deviceEqualsDiffKeys(old Device, d Device) []string {
	keys := []string{}

	for k := range d {
		if d[k] != old[k] {
			keys = append(keys, k)
		}
	}

	for k := range old {
		if d[k] != old[k] {
			keys = append(keys, k)
		}
	}

	return keys
}
