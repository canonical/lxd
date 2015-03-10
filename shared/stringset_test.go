package shared

import (
	"testing"
)

func TestStringSetSubset(t *testing.T) {
	ss := NewStringSet([]string{"one", "two"})

	if !ss.IsSubset(ss) {
		t.Error("subests wrong")
		return
	}

	if !ss.IsSubset(NewStringSet([]string{"one", "two", "three"})) {
		t.Error("subsets wrong")
		return
	}

	if ss.IsSubset(NewStringSet([]string{"four"})) {
		t.Error("subests wrong")
		return
	}
}
