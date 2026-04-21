package device

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/ip"
	"github.com/canonical/lxd/lxd/network"
)

func TestNetworkCalculatePairMTUWithoutParent(t *testing.T) {
	tests := []struct {
		name            string
		deviceConfig    deviceConfig.Device
		expectedHostMTU uint32
		expectedInstMTU uint32
		expectedErr     string
	}{
		{
			name:            "No parent and no MTU",
			deviceConfig:    deviceConfig.Device{},
			expectedHostMTU: 0,
			expectedInstMTU: 0,
		},
		{
			name: "MTU set without parent",
			deviceConfig: deviceConfig.Device{
				"mtu": "1400",
			},
			expectedHostMTU: 1400,
			expectedInstMTU: 1400,
		},
		{
			name: "Invalid MTU",
			deviceConfig: deviceConfig.Device{
				"mtu": "not-an-int",
			},
			expectedErr: "Invalid MTU specified \"not-an-int\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostMTU, instanceMTU, err := networkCalculatePairMTU(tt.deviceConfig)
			if tt.expectedErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedHostMTU, hostMTU)
			assert.Equal(t, tt.expectedInstMTU, instanceMTU)
		})
	}
}

func TestNetworkCalculatePairMTUWithParent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Test requires root privileges to create network interfaces")
	}

	parentName := network.RandomDevName("dmy")
	require.NotEmpty(t, parentName)

	dummy := ip.Dummy{Link: ip.Link{Name: parentName}}
	err := dummy.Add()
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = network.InterfaceRemove(parentName)
	})

	err = (&ip.Link{Name: parentName}).SetMTU(1300)
	require.NoError(t, err)

	tests := []struct {
		name            string
		deviceConfig    deviceConfig.Device
		expectedHostMTU uint32
		expectedInstMTU uint32
	}{
		{
			name: "Inherit parent MTU when mtu not set",
			deviceConfig: deviceConfig.Device{
				"parent": parentName,
			},
			expectedHostMTU: 1300,
			expectedInstMTU: 1300,
		},
		{
			name: "Keep larger parent MTU when mtu is lower",
			deviceConfig: deviceConfig.Device{
				"parent": parentName,
				"mtu":    "1200",
			},
			expectedHostMTU: 1300,
			expectedInstMTU: 1200,
		},
		{
			name: "Raise host MTU when mtu is higher",
			deviceConfig: deviceConfig.Device{
				"parent": parentName,
				"mtu":    "1400",
			},
			expectedHostMTU: 1400,
			expectedInstMTU: 1400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostMTU, instanceMTU, err := networkCalculatePairMTU(tt.deviceConfig)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedHostMTU, hostMTU)
			assert.Equal(t, tt.expectedInstMTU, instanceMTU)
		})
	}
}
