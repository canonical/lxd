package drivers

import (
	"log"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPortRangesFromSlice(t *testing.T) {
	tests := []struct {
		name     string
		ports    []uint64
		expected [][2]uint64
	}{
		{
			name:     "Single port",
			ports:    []uint64{80},
			expected: [][2]uint64{{80, 1}},
		},
		{
			name:     "Single range",
			ports:    []uint64{80, 81, 82, 83},
			expected: [][2]uint64{{80, 4}},
		},
		{
			name:  "Multiple (single) ports",
			ports: []uint64{80, 90, 100},
			expected: [][2]uint64{
				{80, 1},
				{90, 1},
				{100, 1},
			},
		},
		{
			name:  "Multiple ranges",
			ports: []uint64{80, 81, 82, 90, 91, 92, 100, 101, 102},
			expected: [][2]uint64{
				{80, 3},
				{90, 3},
				{100, 3},
			},
		},
		{
			name:  "Mixed ranges and single ports",
			ports: []uint64{80, 81, 82, 87, 90, 91, 92, 88, 100, 101, 102, 89},
			expected: [][2]uint64{
				{80, 3},
				{87, 1},
				{90, 3},
				{88, 1},
				{100, 3},
				{89, 1},
			},
		},
	}
	for i, tt := range tests {
		log.Printf("Running test #%d: %s", i, tt.name)
		ranges := PortRangesFromSlice(tt.ports)
		require.ElementsMatch(t, ranges, tt.expected)
	}
}
