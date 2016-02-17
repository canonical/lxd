package main

import (
	"testing"

	"github.com/lxc/lxd/shared"
)

func TestDotPrefixMatch(t *testing.T) {
	list := listCmd{}

	pass := true
	pass = pass && list.dotPrefixMatch("s.privileged", "security.privileged")
	pass = pass && list.dotPrefixMatch("u.blah", "user.blah")

	if !pass {
		t.Error("failed prefix matching")
	}
}

func TestShouldShow(t *testing.T) {
	list := listCmd{}

	state := &shared.ContainerInfo{
		Name: "foo",
		Config: map[string]string{
			"security.privileged": "1",
			"user.blah":           "abc",
		},
	}

	if !list.shouldShow([]string{"u.blah=abc"}, state) {
		t.Error("u.blah=abc didn't match")
	}

	if !list.shouldShow([]string{"user.blah=abc"}, state) {
		t.Error("user.blah=abc didn't match")
	}

	if list.shouldShow([]string{"bar", "u.blah=abc"}, state) {
		t.Errorf("name filter didn't work")
	}

	if list.shouldShow([]string{"bar", "u.blah=other"}, state) {
		t.Errorf("value filter didn't work")
	}
}
