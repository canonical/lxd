//go:build linux && cgo

package seccomp

import (
	"fmt"
	"os"
	"testing"

	"github.com/canonical/lxd/lxd/cgroup"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/shared/api"
)

type mockInstance struct {
	expandedConfig map[string]string
}

func (m *mockInstance) Name() string                           { return "check-unit" }
func (m *mockInstance) Project() api.Project                   { return api.Project{Name: "default"} }
func (m *mockInstance) ExpandedConfig() map[string]string      { return m.expandedConfig }
func (m *mockInstance) IsPrivileged() bool                     { return false }
func (m *mockInstance) Architecture() int                      { return 0 }
func (m *mockInstance) RootfsPath() string                     { return "" }
func (m *mockInstance) CGroup() (*cgroup.CGroup, error)        { return nil, nil }
func (m *mockInstance) CurrentIdmap() (*idmap.IdmapSet, error) { return nil, nil }
func (m *mockInstance) DiskIdmap() (*idmap.IdmapSet, error)    { return nil, nil }
func (m *mockInstance) IdmappedStorage(path string, fstype string) idmap.IdmapStorageType {
	return idmap.IdmapStorageNone
}

func (m *mockInstance) InsertSeccompUnixDevice(prefix string, mDev deviceConfig.Device, pid int) error {
	return nil
}

func TestInstanceNeedsPolicy(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]string
		expected bool
	}{
		{
			name:     "empty config -> needs policy (default deny)",
			config:   map[string]string{},
			expected: true,
		},
		{
			name: "explicitly disable default deny",
			config: map[string]string{
				"security.syscalls.deny_default": "false",
			},
			expected: false,
		},
		{
			name: "raw seccomp",
			config: map[string]string{
				"raw.seccomp": "some policy",
			},
			expected: true,
		},
		{
			name: "security.syscalls.allow",
			config: map[string]string{
				"security.syscalls.allow": "xxx",
			},
			expected: true,
		},
		{
			name: "security.syscalls.deny",
			config: map[string]string{
				"security.syscalls.deny": "xxx",
			},
			expected: true,
		},
		{
			name: "security.syscalls.deny_compat",
			config: map[string]string{
				"security.syscalls.deny_compat": "true",
			},
			expected: true,
		},
		{
			name: "security.syscalls.intercept.mknod",
			config: map[string]string{
				"security.syscalls.intercept.mknod": "true",
			},
			expected: true,
		},
		{
			name: "security.syscalls.deny_default false but mknod intercept",
			config: map[string]string{
				"security.syscalls.deny_default":    "false",
				"security.syscalls.intercept.mknod": "true",
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &mockInstance{expandedConfig: tc.config}
			if InstanceNeedsPolicy(m) != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, InstanceNeedsPolicy(m))
			}
		})
	}
}

func TestTaskIDs(t *testing.T) {
	pid := os.Getpid()
	uid, gid, _, _, err := TaskIDs(pid)
	if err != nil {
		t.Fatalf("TaskIDs failed: %v", err)
	}

	euid := int64(os.Geteuid())
	egid := int64(os.Getegid())

	if uid != euid {
		t.Errorf("Expected UID %d, got %d", euid, uid)
	}

	if gid != egid {
		t.Errorf("Expected GID %d, got %d", egid, gid)
	}
}

func TestMountFlagsToOpts(t *testing.T) {
	opts := mountFlagsToOpts(knownFlags)
	if opts != "ro,nosuid,nodev,noexec,sync,remount,mand,noatime,nodiratime,bind,strictatime,lazytime" {
		t.Fatal(fmt.Errorf("Mount options parsing failed with invalid option string: %s", opts))
	}

	opts = mountFlagsToOpts(knownFlagsRecursive)
	if opts != "ro,nosuid,nodev,noexec,sync,remount,mand,noatime,nodiratime,rbind,strictatime,lazytime" {
		t.Fatal(fmt.Errorf("Mount options parsing failed with invalid option string: %s", opts))
	}
}
