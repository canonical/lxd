package drivers

import "fmt"

// PortRangesFromSlice checks if adjacent indices in the given slice contain consecutive
// numbers and returns a slice of port ranges ([startNumber, rangeSize]) accordingly.
//
// Note that if the input slice was parsed from multiple ranges (e.g. "80-81,5000") then
// this function will normalise the given ranges (e.g. "80-81,82,5000" will become "80-82-5000").
func PortRangesFromSlice(ports []uint64) [][2]uint64 {
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

func destinationStr(ipVersion uint, addr string, port string) string {
	dest := fmt.Sprintf("%s:%s", addr, port)
	if ipVersion == 6 {
		dest = fmt.Sprintf("[%s]:%s", addr, port)
	}

	return dest
}

func portRangeStr(portRange [2]uint64) string {
	if portRange[1] < 1 {
		return ""
	} else if portRange[1] == 1 {
		return fmt.Sprintf("%d", portRange[0])
	}
	return fmt.Sprintf("%d:%d", portRange[0], portRange[0]+portRange[1]-1)
}
