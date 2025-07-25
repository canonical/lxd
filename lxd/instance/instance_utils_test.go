package instance

import (
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
			expectErr: true,
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
}
