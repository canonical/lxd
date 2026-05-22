package device

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
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
	// "lo" is always present and its MTU is readable from /sys/class/net/lo/mtu
	// without any elevated privileges, so no interface creation is needed.
	loMTU, err := network.GetDevMTU("lo")
	require.NoError(t, err)
	require.Greater(t, loMTU, uint32(200))
	require.LessOrEqual(t, loMTU, ^uint32(0)-uint32(200))

	lowerMTU := loMTU - 200
	higherMTU := loMTU + 200

	tests := []struct {
		name            string
		deviceConfig    deviceConfig.Device
		expectedHostMTU uint32
		expectedInstMTU uint32
	}{
		{
			name: "Inherit parent MTU when mtu not set",
			deviceConfig: deviceConfig.Device{
				"parent": "lo",
			},
			expectedHostMTU: loMTU,
			expectedInstMTU: loMTU,
		},
		{
			name: "Keep larger parent MTU when mtu is lower",
			deviceConfig: deviceConfig.Device{
				"parent": "lo",
				"mtu":    strconv.FormatUint(uint64(lowerMTU), 10),
			},
			expectedHostMTU: loMTU,
			expectedInstMTU: lowerMTU,
		},
		{
			name: "Raise host MTU when mtu is higher",
			deviceConfig: deviceConfig.Device{
				"parent": "lo",
				"mtu":    strconv.FormatUint(uint64(higherMTU), 10),
			},
			expectedHostMTU: higherMTU,
			expectedInstMTU: higherMTU,
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
