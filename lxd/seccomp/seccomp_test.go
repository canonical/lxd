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
	idmapSet       *idmap.IdmapSet
}

func (m *mockInstance) Name() string                           { return "check-unit" }
func (m *mockInstance) Project() api.Project                   { return api.Project{Name: "default"} }
func (m *mockInstance) ExpandedConfig() map[string]string      { return m.expandedConfig }
func (m *mockInstance) IsPrivileged() bool                     { return false }
func (m *mockInstance) Architecture() int                      { return 0 }
func (m *mockInstance) RootfsPath() string                     { return "" }
func (m *mockInstance) CGroup() (*cgroup.CGroup, error)        { return nil, nil }
func (m *mockInstance) CurrentIdmap() (*idmap.IdmapSet, error) { return m.idmapSet, nil }
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

func TestMountHandleHugetlbfsArgs(t *testing.T) {
	// Typical unprivileged container idmap: uid/gid 0-65535 in the namespace => 100000-165535 on the host.
	idmapSet := &idmap.IdmapSet{Idmap: []idmap.IdmapEntry{
		{Isuid: true, Isgid: false, Nsid: 0, Hostid: 100000, Maprange: 65536},
		{Isuid: false, Isgid: true, Nsid: 0, Hostid: 100000, Maprange: 65536},
	}}

	tests := []struct {
		name     string
		fstype   string
		data     string
		idmapSet *idmap.IdmapSet
		nsuid    int64
		nsgid    int64
		wantData string
	}{
		{
			name:     "non-hugetlbfs fstype leaves data unchanged",
			fstype:   "ext4",
			data:     "uid=0",
			wantData: "uid=0",
		},
		{
			name:     "empty data uses nsuid and nsgid",
			fstype:   "hugetlbfs",
			nsuid:    100000,
			nsgid:    100000,
			wantData: "uid=100000,gid=100000",
		},
		{
			name:     "uid and gid are shifted into host namespace",
			fstype:   "hugetlbfs",
			data:     "uid=0,gid=0",
			idmapSet: idmapSet,
			nsuid:    100000,
			nsgid:    100000,
			wantData: "uid=100000,gid=100000",
		},
		{
			name:     "uid only - gid appended from nsgid",
			fstype:   "hugetlbfs",
			data:     "uid=0",
			idmapSet: idmapSet,
			nsuid:    100000,
			nsgid:    100001,
			wantData: "uid=100000,gid=100001",
		},
		{
			name:     "gid only - uid appended from nsuid",
			fstype:   "hugetlbfs",
			data:     "gid=0",
			idmapSet: idmapSet,
			nsuid:    100001,
			nsgid:    100000,
			wantData: "gid=100000,uid=100001",
		},
		{
			name:     "non-numeric uid value is passed through unchanged",
			fstype:   "hugetlbfs",
			data:     "uid=notanumber,gid=0",
			idmapSet: idmapSet,
			wantData: "uid=notanumber,gid=0",
		},
		{
			name:     "non-numeric gid value is passed through unchanged",
			fstype:   "hugetlbfs",
			data:     "gid=notanumber",
			idmapSet: idmapSet,
			wantData: "gid=notanumber",
		},
		{
			// uid outside the mapped range causes ShiftIntoNs to return -1.
			name:     "uid outside idmap range is passed through unchanged",
			fstype:   "hugetlbfs",
			data:     "uid=99999,gid=0",
			idmapSet: idmapSet,
			wantData: "uid=99999,gid=0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &mockInstance{idmapSet: tc.idmapSet}
			args := &MountArgs{fstype: tc.fstype, data: tc.data}
			var s *Server
			err := s.mountHandleHugetlbfsArgs(m, args, tc.nsuid, tc.nsgid)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if args.data != tc.wantData {
				t.Errorf("expected data %q, got %q", tc.wantData, args.data)
			}
		})
	}
}

func TestSyscallInterceptMountFilter(t *testing.T) {
	tests := []struct {
		name      string
		config    map[string]string
		wantMap   map[string]string
		wantError bool
	}{
		{
			name:    "disabled by default",
			config:  map[string]string{},
			wantMap: map[string]string{},
		},
		{
			name: "explicitly disabled",
			config: map[string]string{
				"security.syscalls.intercept.mount": "false",
			},
			wantMap: map[string]string{},
		},
		{
			name: "allowed filesystems only",
			config: map[string]string{
				"security.syscalls.intercept.mount":         "true",
				"security.syscalls.intercept.mount.allowed": "ext4,xfs",
			},
			wantMap: map[string]string{"ext4": "", "xfs": ""},
		},
		{
			name: "fuse mapping",
			config: map[string]string{
				"security.syscalls.intercept.mount":      "true",
				"security.syscalls.intercept.mount.fuse": "ext4=/usr/bin/fuse2fs",
			},
			wantMap: map[string]string{"ext4": "/usr/bin/fuse2fs"},
		},
		{
			// strings.Cut splits on the first '=' only, so a fuse binary
			// path containing '=' is accepted. It won't work but that's a
			// configuration error.
			name: "fuse binary path with extra equals sign",
			config: map[string]string{
				"security.syscalls.intercept.mount":      "true",
				"security.syscalls.intercept.mount.fuse": "ext4=/usr/bin/fuse2fs=debug",
			},
			wantMap: map[string]string{"ext4": "/usr/bin/fuse2fs=debug"},
		},
		{
			name: "fuse entry missing equals sign returns error",
			config: map[string]string{
				"security.syscalls.intercept.mount":      "true",
				"security.syscalls.intercept.mount.fuse": "ext4",
			},
			wantError: true,
		},
		{
			name: "filesystem in both allowed and fuse returns error",
			config: map[string]string{
				"security.syscalls.intercept.mount":         "true",
				"security.syscalls.intercept.mount.fuse":    "ext4=/usr/bin/fuse2fs",
				"security.syscalls.intercept.mount.allowed": "ext4",
			},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SyscallInterceptMountFilter(tc.config)
			if tc.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(got) != len(tc.wantMap) {
				t.Fatalf("expected map %v, got %v", tc.wantMap, got)
			}

			for k, v := range tc.wantMap {
				if got[k] != v {
					t.Errorf("expected fsMap[%q]=%q, got %q", k, v, got[k])
				}
			}
		})
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
