package util

// CompareConfigs compares two config maps and returns true if they are equal.
func CompareConfigs(config1, config2 map[string]string) bool {
	for key, value := range config1 {
		if config2[key] != value {
			return false
		}
	}
	for key, value := range config2 {
		if config1[key] != value {
			return false
		}
	}
	return true
}
