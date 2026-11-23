package instance

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_lxcParseRawLXC(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		key       string
		val       string
		expectErr bool
	}{
		{
			name:      "ValidConfig",
			line:      `lxc.mount.entry="/dev/cgroup cgroup /sys/fs/cgroup cgroup rw,relatime,mode=755 0 0"`,
			key:       "lxc.mount.entry",
			val:       `"/dev/cgroup cgroup /sys/fs/cgroup cgroup rw,relatime,mode=755 0 0"`,
			expectErr: false,
		},
		{
			name:      "Invalid separator",
			line:      `lxc.mount.entry: "/dev/cgroup"`,
			key:       "",
			val:       "",
			expectErr: true,
		},
		{
			name:      "EmptyConfig",
			line:      "",
			key:       "",
			val:       "",
			expectErr: false,
		},
		{
			name:      "WhitespaceOnlyConfig",
			line:      "    ",
			key:       "",
			val:       "",
			expectErr: true,
		},
		{
			name:      "CommentOnlyConfig",
			line:      "# This is a comment",
			key:       "",
			val:       "",
			expectErr: false,
		},
	}

	for _, test := range tests {
		t.Log("Running test case:", test.name)
		key, val, err := lxcParseRawLXC(test.line)
		if test.expectErr {
			assert.Error(t, err, "Expected error for test case: %s", test.name)
		} else {
			assert.NoError(t, err, "Did not expect error for test case: %s", test.name)
		}

		assert.Equal(t, test.key, key, "Unexpected key for test case: %s", test.name)
		assert.Equal(t, test.val, val, "Unexpected value for test case: %s", test.name)
	}
}

func Test_lxcValidConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		expectErr bool
	}{
		{
			name:      "ValidConfig",
			config:    `lxc.mount.entry="/dev/cgroup cgroup /sys/fs/cgroup cgroup rw,relatime,mode=755 0 0"`,
			expectErr: false,
		},
		{
			name:      "Illegal log file",
			config:    `lxc.log.file = "/dev/null"`,
			expectErr: true,
		},
		{
			name:      "Illegal syslog",
			config:    `lxc.log.syslog = "true"`,
			expectErr: true,
		},
		{
			name:      "Illegal ephemeral",
			config:    `lxc.ephemeral = "true"`,
			expectErr: true,
		},
		{
			name:      "Illegal prlimit",
			config:    `lxc.prlimit.nice = "10"`,
			expectErr: true,
		},
		{
			name:      "Allowed nice limit",
			config:    `lxc.kernel.nice = "10"`,
			expectErr: false,
		},
		{
			name:      "Global network config",
			config:    `lxc.net.ipv4.address=192.0.2.2`,
			expectErr: false,
		},
		{
			name:      "Interface specific network config",
			config:    `lxc.net.0.ipv4.address=192.0.2.2`,
			expectErr: false,
		},
		{
			name: "InvalidConfig",
			config: `
# invalid config
lxc.log.file = "/dev/null"`,
			expectErr: true,
		},
	}

	for _, test := range tests {
		t.Log("Running test case:", test.name)
		err := lxcValidConfig(test.config)
		if test.expectErr {
			assert.Error(t, err, "Expected error for test case: %s", test.name)
		} else {
			assert.NoError(t, err, "Did not expect error for test case: %s", test.name)
		}
	}

	err := lxcValidConfig(`lxc.idmap="u 0 0 1000"`)
	assert.NoError(t, err, "Expected no error for valid unprivileged mapping")

	t.Setenv("LXD_UNPRIVILEGED_ONLY", "1")
	err = lxcValidConfig(`lxc.idmap="u 0 0 1000"`)
	assert.Error(t, err, "Expected error for unprivileged mapping with LXD_UNPRIVILEGED_ONLY set")
}

func Test_DeviceNextInterfaceHWAddr(t *testing.T) {
	mac1, err := DeviceNextInterfaceHWAddr()
	if err != nil {
		t.Fatalf("Failed getting next interface HW address: %v", err)
	}

	assert.Len(t, mac1, 17, "Expected MAC address to be 17 characters long")
	if !strings.HasPrefix(mac1, "00:16:3e:") {
		t.Fatalf("Expected MAC address to start with '00:16:3e:', got %s", mac1)
	}

	mac2, err := DeviceNextInterfaceHWAddr()
	if err != nil {
		t.Fatalf("Failed getting next interface HW address: %v", err)
	}

	assert.Len(t, mac2, 17, "Expected MAC address to be 17 characters long")
	if !strings.HasPrefix(mac2, "00:16:3e:") {
		t.Fatalf("Expected MAC address to start with '00:16:3e:', got %s", mac2)
	}

	assert.NotEqual(t, mac1, mac2, "Expected different MAC addresses for subsequent calls")
}

func Benchmark_DeviceNextInterfaceHWAddr(b *testing.B) {
	for b.Loop() {
		_, err := DeviceNextInterfaceHWAddr()
		if err != nil {
			b.Fatalf("Failed getting next interface HW address: %v", err)
		}
	}
}

func TestDiskPropsFromSource(t *testing.T) {
	both := map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}
	none := map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""}

	tests := []struct {
		name         string
		source       map[string]string
		carriesState bool
		want         map[string]string
	}{
		{"saved state carries both keys", both, true, both},
		{"no saved state carries neither key", both, false, none},
		{"queues only", map[string]string{"volatile.disk_queues": "4"}, true, map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": ""}},
		{"iothreads only", map[string]string{"volatile.disk_iothreads": "1"}, true, map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": "1"}},
		{"recorded zero is kept", map[string]string{"volatile.disk_queues": "0", "volatile.disk_iothreads": "0"}, true, map[string]string{"volatile.disk_queues": "0", "volatile.disk_iothreads": "0"}},
		{"source without the keys gives none", map[string]string{"limits.cpu": "2"}, true, none},
		{"nil source gives none", nil, true, none},
		{"queues out of range", map[string]string{"volatile.disk_queues": "65", "volatile.disk_iothreads": "1"}, true, map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": "1"}},
		{"iothreads out of range", map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "2"}, true, map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": ""}},
		{"not a number", map[string]string{"volatile.disk_queues": "many", "volatile.disk_iothreads": "yes"}, true, none},
		{"negative", map[string]string{"volatile.disk_queues": "-1", "volatile.disk_iothreads": "-1"}, true, none},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DiskPropsFromSource(tt.source, tt.carriesState))
		})
	}
}

func TestRefreshDiskPropsChanges(t *testing.T) {
	both := map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}
	other := map[string]string{"volatile.disk_queues": "2", "volatile.disk_iothreads": "0"}

	tests := []struct {
		name     string
		source   map[string]string
		target   map[string]string
		copyKeys bool
		want     map[string]string
	}{
		{"source with a saved state replaces the keys of the target", both, other, true, map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}},
		{"same keys change nothing", both, both, true, map[string]string{}},
		{"target without keys gets the keys of the source", both, map[string]string{}, true, map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}},
		{"source without keys removes the keys of the target", map[string]string{}, other, true, map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""}},
		{"source with one key removes the other key of the target", map[string]string{"volatile.disk_queues": "4"}, both, true, map[string]string{"volatile.disk_iothreads": ""}},
		{"invalid value of the source is not taken", map[string]string{"volatile.disk_queues": "999", "volatile.disk_iothreads": "1"}, both, true, map[string]string{"volatile.disk_queues": ""}},
		{"source without a saved state keeps the keys of the target", both, other, false, map[string]string{}},
		{"source without a saved state and target without keys", both, map[string]string{}, false, map[string]string{}},
		{"nil source with a saved state removes the keys of the target", nil, other, true, map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RefreshDiskPropsChanges(tt.source, tt.target, tt.copyKeys))
		})
	}
}

func TestApplyDiskProps(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]string
		props  map[string]string
		want   map[string]string
	}{
		{
			"values are set",
			map[string]string{"limits.cpu": "2"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			map[string]string{"limits.cpu": "2", "volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"values replace the ones of the request",
			map[string]string{"volatile.disk_queues": "64", "volatile.disk_iothreads": "0"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"empty values remove both keys",
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1", "limits.cpu": "2"},
			map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""},
			map[string]string{"limits.cpu": "2"},
		},
		{
			"one value set and one removed",
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": ""},
			map[string]string{"volatile.disk_queues": "4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ApplyDiskProps(tt.config, tt.props)
			assert.Equal(t, tt.want, tt.config)
		})
	}
}
