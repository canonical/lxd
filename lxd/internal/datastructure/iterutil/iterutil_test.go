package iterutil

import (
	"maps"
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

func TestFilter2(t *testing.T) {
	nonZeroKey := func(k int, _ string) bool { return k != 0 }

	m := map[int]string{0: "default", 1: "one", 2: "two"}
	got := maps.Collect(Filter2(maps.All(m), nonZeroKey))
	assert.Equal(t, map[int]string{1: "one", 2: "two"}, got)

	// Both nil and empty input maps produce an empty (non-nil) result.
	assert.Equal(t, map[int]string{}, maps.Collect(Filter2(maps.All[map[int]string](nil), nonZeroKey)))
	assert.Equal(t, map[int]string{}, maps.Collect(Filter2(maps.All(map[int]string{}), nonZeroKey)))

	// Stops pulling from seq once yield returns false (early termination via break).
	visited := 0
	for range Filter2(maps.All(m), nonZeroKey) {
		visited++
		break
	}

	assert.Equal(t, 1, visited)
}
