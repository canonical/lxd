package drivers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCephBuildMount(t *testing.T) {
	tests := []struct {
		name              string
		user              string
		key               string
		fsid              string
		monitors          Monitors
		fsName            string
		path              string
		msMode            string
		modernMountSyntax bool
		wantSource        string
		wantOptions       []string
	}{
		{
			name: "V2 monitors with key",
			user: "admin",
			key:  "AQBxyz==",
			fsid: "abc-def-123",
			monitors: Monitors{
				V1: []string{"10.0.0.1:6789"},
				V2: []string{"10.0.0.1:3300"},
			},
			fsName:            "myfs",
			path:              "/",
			msMode:            "prefer-secure",
			modernMountSyntax: true,
			wantSource:        "admin@abc-def-123.myfs=/",
			wantOptions: []string{
				"mon_addr=10.0.0.1:3300",
				"name=admin",
				"secret=AQBxyz==",
				"ms_mode=prefer-secure",
			},
		},
		{
			name: "V1 only monitors",
			user: "admin",
			key:  "AQBxyz==",
			fsid: "abc-def-123",
			monitors: Monitors{
				V1: []string{"10.0.0.1:6789", "10.0.0.2:6789"},
			},
			fsName:            "myfs",
			path:              "/subdir",
			msMode:            "prefer-secure",
			modernMountSyntax: true,
			wantSource:        "admin@abc-def-123.myfs=/subdir",
			wantOptions: []string{
				"mon_addr=10.0.0.1:6789/10.0.0.2:6789",
				"name=admin",
				"secret=AQBxyz==",
				"ms_mode=prefer-secure",
			},
		},
		{
			name: "V1 only monitors (legacy mount syntax)",
			user: "admin",
			key:  "AQBxyz==",
			fsid: "abc-def-123",
			monitors: Monitors{
				V1: []string{"10.0.0.1:6789", "10.0.0.2:6789"},
			},
			fsName:            "myfs",
			path:              "/subdir",
			msMode:            "prefer-secure",
			modernMountSyntax: false,
			wantSource:        "10.0.0.1:6789,10.0.0.2:6789:/subdir",
			wantOptions: []string{
				"mon_addr=10.0.0.1:6789/10.0.0.2:6789",
				"name=admin",
				"secret=AQBxyz==",
				"ms_mode=prefer-secure",
				"mds_namespace=myfs",
				"fsid=abc-def-123",
			},
		},
		{
			name: "Multiple V2 monitors",
			user: "client1",
			key:  "secret123",
			fsid: "uuid-456",
			monitors: Monitors{
				V1: []string{"10.0.0.1:6789"},
				V2: []string{"10.0.0.1:3300", "10.0.0.2:3300", "10.0.0.3:3300"},
			},
			fsName:            "cephfs",
			path:              "/data/pool",
			msMode:            "prefer-secure",
			modernMountSyntax: true,
			wantSource:        "client1@uuid-456.cephfs=/data/pool",
			wantOptions: []string{
				"mon_addr=10.0.0.1:3300/10.0.0.2:3300/10.0.0.3:3300",
				"name=client1",
				"secret=secret123",
				"ms_mode=prefer-secure",
			},
		},
		{
			name: "No key (cephx disabled)",
			user: "admin",
			key:  "",
			fsid: "abc-def-123",
			monitors: Monitors{
				V2: []string{"10.0.0.1:3300"},
			},
			fsName:            "myfs",
			path:              "/",
			msMode:            "prefer-secure",
			modernMountSyntax: true,
			wantSource:        "admin@abc-def-123.myfs=/",
			wantOptions: []string{
				"mon_addr=10.0.0.1:3300",
				"name=admin",
				"ms_mode=prefer-secure",
			},
		},
		{
			name: "Path without leading slash",
			user: "admin",
			key:  "AQBxyz==",
			fsid: "abc-def-123",
			monitors: Monitors{
				V1: []string{"10.0.0.1:6789"},
			},
			fsName:            "myfs",
			path:              "subdir",
			msMode:            "prefer-secure",
			modernMountSyntax: true,
			wantSource:        "admin@abc-def-123.myfs=/subdir",
			wantOptions: []string{
				"mon_addr=10.0.0.1:6789",
				"name=admin",
				"secret=AQBxyz==",
				"ms_mode=prefer-secure",
			},
		},
		{
			name: "Empty path",
			user: "admin",
			key:  "AQBxyz==",
			fsid: "abc-def-123",
			monitors: Monitors{
				V1: []string{"10.0.0.1:6789"},
			},
			fsName:            "myfs",
			path:              "",
			msMode:            "prefer-secure",
			modernMountSyntax: true,
			wantSource:        "admin@abc-def-123.myfs=/",
			wantOptions: []string{
				"mon_addr=10.0.0.1:6789",
				"name=admin",
				"secret=AQBxyz==",
				"ms_mode=prefer-secure",
			},
		},
		{
			name: "Strict secure mode",
			user: "admin",
			key:  "AQBxyz==",
			fsid: "abc-def-123",
			monitors: Monitors{
				V2: []string{"10.0.0.1:3300"},
			},
			fsName:            "myfs",
			path:              "/",
			msMode:            "secure",
			modernMountSyntax: true,
			wantSource:        "admin@abc-def-123.myfs=/",
			wantOptions: []string{
				"mon_addr=10.0.0.1:3300",
				"name=admin",
				"secret=AQBxyz==",
				"ms_mode=secure",
			},
		},
		{
			name: "Prefer CRC mode",
			user: "admin",
			key:  "AQBxyz==",
			fsid: "abc-def-123",
			monitors: Monitors{
				V2: []string{"10.0.0.1:3300"},
			},
			fsName:            "myfs",
			path:              "/",
			msMode:            "prefer-crc",
			modernMountSyntax: true,
			wantSource:        "admin@abc-def-123.myfs=/",
			wantOptions: []string{
				"mon_addr=10.0.0.1:3300",
				"name=admin",
				"secret=AQBxyz==",
				"ms_mode=prefer-crc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, options := CephBuildMount(tt.user, tt.key, tt.fsid, tt.monitors, tt.fsName, tt.path, tt.msMode, tt.modernMountSyntax)
			assert.Equal(t, tt.wantSource, source)
			assert.Equal(t, tt.wantOptions, options)
		})
	}
}

func TestGetCephKeyFromFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
		wantErr bool
	}{
		{
			name: "Standard keyring format",
			content: `[client.admin]
	key = AQBxyz123456789==
	caps mds = "allow *"
	caps mon = "allow *"
	caps osd = "allow *"
`,
			wantKey: "AQBxyz123456789==",
		},
		{
			name: "Key without spaces around equals",
			content: `[client.admin]
	key=AQBnoSpaces==
`,
			wantKey: "AQBnoSpaces==",
		},
		{
			name: "Key with extra whitespace",
			content: `[client.admin]
	key =   AQBextraSpaces==   
`,
			wantKey: "AQBextraSpaces==",
		},
		{
			name: "No key entry",
			content: `[client.admin]
	caps mds = "allow *"
`,
			wantErr: true,
		},
		{
			name:    "Empty file",
			content: "",
			wantErr: true,
		},
		{
			name: "Key line without value",
			content: `[client.admin]
	key
`,
			wantErr: true,
		},
		{
			name: "Key with blank lines",
			content: `
[client.admin]

	key = AQBblankLines==

	caps mds = "allow *"
`,
			wantKey: "AQBblankLines==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write the keyring content to a temp file.
			dir := t.TempDir()
			keyringPath := filepath.Join(dir, "test.keyring")
			err := os.WriteFile(keyringPath, []byte(tt.content), 0600)
			require.NoError(t, err)

			key, err := getCephKeyFromFile(keyringPath)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantKey, key)
		})
	}
}

func TestGetCephKeyFromFile_nonexistent(t *testing.T) {
	_, err := getCephKeyFromFile("/nonexistent/path/keyring")
	assert.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
