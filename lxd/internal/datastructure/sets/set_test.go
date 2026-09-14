package sets

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	assert.ElementsMatch(t, []string{"a", "b"}, New("a", "b", "a").Slice())
	assert.Empty(t, New[string]().Slice())
}

func TestFromSeq(t *testing.T) {
	got := FromSeq(slices.Values([]int{1, 2, 2, 3}))
	assert.ElementsMatch(t, []int{1, 2, 3}, got.Slice())

	// Composes with maps.Keys to build a set from a map's keys.
	fromKeys := FromSeq(maps.Keys(map[int]string{1: "one", 2: "two"}))
	assert.ElementsMatch(t, []int{1, 2}, fromKeys.Slice())

	// Both nil and empty input slices produce an empty (non-nil) set.
	assert.Empty(t, FromSeq(slices.Values[[]int](nil)))
	assert.Empty(t, FromSeq(slices.Values([]int{})))
}

func TestSetAdd(t *testing.T) {
	s := New("a")
	s.Add("b", "c")
	assert.ElementsMatch(t, []string{"a", "b", "c"}, s.Slice())

	// Adding an item already present is a no-op, not a duplicate.
	s.Add("a")
	assert.ElementsMatch(t, []string{"a", "b", "c"}, s.Slice())

	// Add with no arguments changes nothing.
	s.Add()
	assert.ElementsMatch(t, []string{"a", "b", "c"}, s.Slice())
}

func TestSetRemove(t *testing.T) {
	s := New("a", "b", "c")
	s.Remove("b")
	assert.ElementsMatch(t, []string{"a", "c"}, s.Slice())

	// Removing an item not present is a no-op, not an error.
	s.Remove("z")
	assert.ElementsMatch(t, []string{"a", "c"}, s.Slice())

	s.Remove("a", "c")
	assert.Empty(t, s.Slice())
}

func TestSetContains(t *testing.T) {
	s := New("a", "b")
	assert.True(t, s.Contains("a"))
	assert.False(t, s.Contains("z"))

	// A nil Set reads like an empty one.
	var nilSet Set[string]
	assert.False(t, nilSet.Contains("a"))
}

func TestSetLen(t *testing.T) {
	assert.Equal(t, 2, New("a", "b").Len())
	assert.Equal(t, 0, New[string]().Len())

	var nilSet Set[string]
	assert.Equal(t, 0, nilSet.Len())
}

func TestSetAll(t *testing.T) {
	s := New(1, 2, 3)

	var visited []int
	for v := range s.All() {
		visited = append(visited, v)
	}

	assert.ElementsMatch(t, []int{1, 2, 3}, visited)

	// A nil Set yields nothing.
	var nilSet Set[int]
	for range nilSet.All() {
		t.Fatal("nil Set's All() should yield nothing")
	}

	// Stops pulling once yield returns false (early termination via break).
	visited = nil
	for v := range s.All() {
		visited = append(visited, v)
		break
	}

	assert.Len(t, visited, 1)
}

func TestSetSlice(t *testing.T) {
	assert.ElementsMatch(t, []string{"a", "b"}, New("a", "b").Slice())

	var nilSet Set[string]
	assert.Empty(t, nilSet.Slice())
}

func TestSetEqual(t *testing.T) {
	assert.True(t, New(1, 2, 3).Equal(New(3, 2, 1)))
	assert.False(t, New(1, 2).Equal(New(1, 2, 3)))
	assert.False(t, New(1, 2, 3).Equal(New(1, 2, 4)))

	// An empty Set and a nil Set are equal to each other and to themselves.
	var nilSet Set[int]
	assert.True(t, nilSet.Equal(New[int]()))
	assert.True(t, New[int]().Equal(nilSet))
}

func TestSetIsSubsetOf(t *testing.T) {
	assert.True(t, New(1, 2).IsSubsetOf(New(1, 2, 3)))
	assert.True(t, New(1, 2, 3).IsSubsetOf(New(1, 2, 3)), "a set is a subset of itself")
	assert.False(t, New(1, 4).IsSubsetOf(New(1, 2, 3)))

	// An empty (including nil) Set is a subset of anything, including another empty Set.
	var nilSet Set[int]
	assert.True(t, nilSet.IsSubsetOf(New(1, 2, 3)))
	assert.True(t, nilSet.IsSubsetOf(New[int]()))
}

func TestSetUnion(t *testing.T) {
	a := New(1, 2)
	b := New(2, 3)

	got := a.Union(b)
	assert.ElementsMatch(t, []int{1, 2, 3}, got.Slice())

	// Neither input is modified.
	assert.ElementsMatch(t, []int{1, 2}, a.Slice())
	assert.ElementsMatch(t, []int{2, 3}, b.Slice())

	assert.ElementsMatch(t, []int{1, 2}, a.Union(New[int]()).Slice())
}

func TestSetIntersect(t *testing.T) {
	a := New(1, 2, 3)
	b := New(2, 3, 4)

	got := a.Intersect(b)
	assert.ElementsMatch(t, []int{2, 3}, got.Slice())

	// Neither input is modified.
	assert.ElementsMatch(t, []int{1, 2, 3}, a.Slice())
	assert.ElementsMatch(t, []int{2, 3, 4}, b.Slice())

	assert.Empty(t, a.Intersect(New(5, 6)).Slice())
}

func TestSetDifference(t *testing.T) {
	a := New(1, 2, 3)
	b := New(2, 3, 4)

	got := a.Difference(b)
	assert.ElementsMatch(t, []int{1}, got.Slice())

	// Neither input is modified.
	assert.ElementsMatch(t, []int{1, 2, 3}, a.Slice())
	assert.ElementsMatch(t, []int{2, 3, 4}, b.Slice())

	// Removing everything leaves an empty set.
	assert.Empty(t, a.Difference(a).Slice())

	// Disjoint sets: difference equals the original.
	assert.ElementsMatch(t, []int{1, 2, 3}, a.Difference(New(7, 8)).Slice())
}
