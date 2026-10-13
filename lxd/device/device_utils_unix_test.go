package device

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/sys"
)

var unixDeviceFileTests = []struct {
	name       string
	files      []string
	deviceName string
	optPrefix  string
	ourFile    string
	ourTarget  string
}{
	{
		name:       "Device name prefix of another device",
		files:      []string{"unix.foo1.dev-foo1", "unix.foo13.dev-foo13"},
		deviceName: "foo1",
		ourFile:    "unix.foo1.dev-foo1",
		ourTarget:  "dev/foo1",
	},
	{
		name:       "Specific path prefix of another path",
		files:      []string{"unix.hp.dev-ttyACM2", "unix.hp.dev-ttyACM28"},
		deviceName: "hp",
		optPrefix:  "dev/ttyACM2",
		ourFile:    "unix.hp.dev-ttyACM2",
		ourTarget:  "dev/ttyACM2",
	},
}

func TestUnixDeviceRemove(t *testing.T) {
	for _, tt := range unixDeviceFileTests {
		t.Run(tt.name, func(t *testing.T) {
			devicesPath := t.TempDir()
			for _, file := range tt.files {
				err := os.Symlink("/dev/null", filepath.Join(devicesPath, file))
				require.NoError(t, err)
			}

			runConf := deviceConfig.RunConfig{}
			err := unixDeviceRemove(devicesPath, "unix", tt.deviceName, tt.optPrefix, &runConf)
			require.NoError(t, err)

			assert.Equal(t, []deviceConfig.MountEntryItem{{TargetPath: tt.ourTarget}}, runConf.Mounts)
			assert.Len(t, runConf.CGroups, 1)
		})
	}
}

func TestUnixDeviceDeleteFiles(t *testing.T) {
	s := &state.State{OS: &sys.OS{}}

	for _, tt := range unixDeviceFileTests {
		t.Run(tt.name, func(t *testing.T) {
			devicesPath := t.TempDir()
			for _, file := range tt.files {
				err := os.WriteFile(filepath.Join(devicesPath, file), nil, 0600)
				require.NoError(t, err)
			}

			err := unixDeviceDeleteFiles(s, devicesPath, "unix", tt.deviceName, tt.optPrefix)
			require.NoError(t, err)

			for _, file := range tt.files {
				_, err := os.Lstat(filepath.Join(devicesPath, file))
				if file == tt.ourFile {
					assert.ErrorIs(t, err, os.ErrNotExist)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}
