package types

func deviceEquals(old Device, d Device) bool {
	// Check for any difference and addition/removal of properties
	for k := range d {
		if d[k] != old[k] {
			return false
		}
	}

	for k := range old {
		if d[k] != old[k] {
			return false
		}
	}

	return true
}

func deviceEqualsDiffKeys(old Device, d Device) []string {
	keys := []string{}

	// Check for any difference and addition/removal of properties
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
