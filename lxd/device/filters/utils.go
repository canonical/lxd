package filters

// Filter represents an arbitrary filter function which takes the devices config as input
// and returns either true or false depending on the filter.
type Filter func(device map[string]string) bool

// Or can be used to evaulate multiple filters using an or operation.
func Or(filters ...Filter) Filter {
	return func(device map[string]string) bool {
		for _, filter := range filters {
			if filter(device) {
				return true
			}
		}

		return false
	}
}

// Not negates the result of the given filter.
func Not(filter Filter) Filter {
	return func(device map[string]string) bool {
		return !filter(device)
	}
}
