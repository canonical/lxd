package main

import (
	"testing"

	"github.com/lxc/lxd/shared"
)

func TestDotPrefixMatch(t *testing.T) {
	pass := true

	pass = pass && dotPrefixMatch("s.privileged", "security.privileged")
	pass = pass && dotPrefixMatch("u.blah", "user.blah")

	if !pass {
		t.Error("failed prefix matching")
	}
}

func TestShouldShow(t *testing.T) {
	state := &shared.ContainerState{
		Name: "foo",
		Config: map[string]string{
			"security.privileged": "1",
			"user.blah":           "abc",
		},
	}

	if !shouldShow([]string{"u.blah=abc"}, state) {
		t.Error("u.blah=abc didn't match")
	}

	if !shouldShow([]string{"user.blah=abc"}, state) {
		t.Error("user.blah=abc didn't match")
	}

	if shouldShow([]string{"bar", "u.blah=abc"}, state) {
		t.Errorf("name filter didn't work")
	}

	if shouldShow([]string{"bar", "u.blah=other"}, state) {
		t.Errorf("value filter didn't work")
	}
}
