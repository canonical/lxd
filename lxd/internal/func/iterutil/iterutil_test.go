package iterutil

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilter(t *testing.T) {
	even := func(n int) bool { return n%2 == 0 }

	got := slices.Collect(Filter(slices.Values([]int{1, 2, 3, 4, 5, 6}), even))
	assert.Equal(t, []int{2, 4, 6}, got)

	// Both nil and empty input slices produce an empty result (slices.Collect returns nil for
	// an empty sequence, regardless of whether the underlying input was nil or empty).
	assert.Empty(t, slices.Collect(Filter(slices.Values[[]int](nil), even)))
	assert.Empty(t, slices.Collect(Filter(slices.Values([]int{}), even)))

	// Stops pulling from seq once yield returns false (early termination via break).
	var visited []int
	for v := range Filter(slices.Values([]int{1, 2, 3, 4, 5}), even) {
		visited = append(visited, v)
		break
	}

	assert.Equal(t, []int{2}, visited)
}
