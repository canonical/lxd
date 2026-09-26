package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsRemoteStorageDriver(t *testing.T) {
	tests := []struct {
		driver   string
		expected bool
	}{
		// Local drivers (must return false)
		{driver: "zfs", expected: false},
		{driver: "btrfs", expected: false},
		{driver: "dir", expected: false},
		{driver: "lvm", expected: false},
		{driver: "custom-local", expected: false},
		{driver: "", expected: false},

		// Remote drivers (must return true)
		{driver: "ceph", expected: true},
		{driver: "cephfs", expected: true},
		{driver: "cephobject", expected: true},
		{driver: "powerflex", expected: true},
		{driver: "pure", expected: true},
		{driver: "powerstore", expected: true},
	}

	for _, tc := range tests {
		t.Run(tc.driver, func(t *testing.T) {
			actual := isRemoteStorageDriver(tc.driver)
			assert.Equal(t, tc.expected, actual, "Driver %q remote check failed", tc.driver)
		})
	}
}
