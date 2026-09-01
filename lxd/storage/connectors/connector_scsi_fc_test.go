package connectors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_fcInitiatorWWPNs(t *testing.T) {
	// newFCHostTree builds a fake "/sys/class/fc_host" containing one directory per
	// entry, holding a "port_name" file unless the value is empty.
	newFCHostTree := func(t *testing.T, hosts map[string]string) string {
		t.Helper()

		dir := t.TempDir()
		for host, portName := range hosts {
			hostDir := filepath.Join(dir, host)
			require.NoError(t, os.MkdirAll(hostDir, 0700))

			if portName == "" {
				// An entry without a port_name, which must be skipped.
				continue
			}

			require.NoError(t, os.WriteFile(filepath.Join(hostDir, "port_name"), []byte(portName), 0600))
		}

		return dir
	}

	tests := []struct {
		name      string
		hosts     map[string]string
		want      []string
		wantError string
	}{
		{
			name:  "Single initiator",
			hosts: map[string]string{"host6": "0x21000024ff43b10c\n"},
			want:  []string{"21000024ff43b10c"},
		},
		{
			// The directory order here is the reverse of the WWPN order, so a stable
			// result can only come from sorting the WWPNs themselves.
			name: "Result is sorted, not in directory order",
			hosts: map[string]string{
				"host0": "0x21000024ff43b10d\n",
				"host1": "0x21000024ff43b10c\n",
			},
			want: []string{"21000024ff43b10c", "21000024ff43b10d"},
		},
		{
			name: "Ports of several adapter cards are all reported",
			hosts: map[string]string{
				"host6": "0x21000024ff43b10c\n",
				"host7": "0x21000024ff43b10d\n",
				"host8": "0x21000024ff43b20a\n",
			},
			want: []string{"21000024ff43b10c", "21000024ff43b10d", "21000024ff43b20a"},
		},
		{
			// The same port reported in two formats must count once.
			name: "Duplicates are removed regardless of format",
			hosts: map[string]string{
				"host6": "0x21000024ff43b10c\n",
				"host7": "21:00:00:24:FF:43:B1:0C\n",
			},
			want: []string{"21000024ff43b10c"},
		},
		{
			name: "Entries without a port_name are skipped",
			hosts: map[string]string{
				"host6": "0x21000024ff43b10c\n",
				"host7": "",
			},
			want: []string{"21000024ff43b10c"},
		},
		{
			name: "Entries with an empty port_name are skipped",
			hosts: map[string]string{
				"host6": "0x21000024ff43b10c\n",
				"host7": "  \n",
			},
			want: []string{"21000024ff43b10c"},
		},
		{
			name:      "No initiators at all",
			hosts:     map[string]string{},
			wantError: "No FC host initiators found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wwpns, err := fcInitiatorWWPNs(newFCHostTree(t, test.hosts))
			if test.wantError != "" {
				assert.EqualError(t, err, test.wantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, wwpns)
		})
	}
}

func Test_fcInitiatorWWPNs_missingDirectory(t *testing.T) {
	// A host with no Fibre Channel support at all must produce a clear error rather
	// than an empty result.
	missing := filepath.Join(t.TempDir(), "absent")

	_, err := fcInitiatorWWPNs(missing)
	assert.ErrorContains(t, err, "No FC hosts found")
	assert.ErrorContains(t, err, missing)
}

func Test_normalizeWWPN(t *testing.T) {
	tests := []struct {
		name string
		wwpn string
		want string
	}{
		{
			name: "Linux sysfs format with 0x prefix",
			wwpn: "0x210034800d7035b3",
			want: "210034800d7035b3",
		},
		{
			name: "Colon-separated byte format",
			wwpn: "21:00:34:80:0d:70:35:b3",
			want: "210034800d7035b3",
		},
		{
			name: "Uppercase WWPN",
			wwpn: "0x210034800D7035B3",
			want: "210034800d7035b3",
		},
		{
			name: "Surrounding whitespace",
			wwpn: "  0x210034800d7035b3  ",
			want: "210034800d7035b3",
		},
		{
			name: "Plain hex without prefix or separators",
			wwpn: "210034800d7035b3",
			want: "210034800d7035b3",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, normalizeWWPN(test.wwpn))
		})
	}
}
