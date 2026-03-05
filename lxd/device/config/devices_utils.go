package config

// deviceEqualsDiffKeys checks for any difference and addition/removal of properties and returns a list of changes.
func deviceEqualsDiffKeys(old Device, d Device) []string {
	var keys []string

	for k := range d {
		if d[k] != old[k] {
			keys = append(keys, k)
		}
	}

	for k := range old {
		_, found := d[k]
		if !found {
			keys = append(keys, k)
		}
	}

	return keys
}
