package drivers

// portRangesFromSlice checks if adjacent indices in the given slice contain consecutive
// numbers and returns a slice of port ranges ([startNumber, rangeSize]) accordingly.
//
// Note that this function cannot differentiate ranges from adjacent ports e.g. if the given
// slice is "[80,81,82]" then the returned range will be "80-82", regardless of whether the
// user input was parsed from "80-82" or "80,81,82".
func portRangesFromSlice(ports []uint64) [][2]uint64 {
	if len(ports) == 0 {
		return nil
	}

	portRanges := make([][2]uint64, 0, len(ports))
	startIdx := 0
	size := uint64(0)
	for i := range ports {
		if i == len(ports)-1 || ports[i+1] != ports[i]+1 {
			size = ports[i] - ports[startIdx] + 1
			portRanges = append(portRanges, [2]uint64{ports[startIdx], size})
			startIdx = i + 1
		}
	}

	return portRanges
}
