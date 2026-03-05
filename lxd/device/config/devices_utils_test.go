package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeviceEqualsDiffKeys(t *testing.T) {
	tests := []struct {
		name         string
		old          Device
		new          Device
		expectedKeys []string
	}{
		{
			name:         "IdenticalDevices",
			old:          Device{"type": "nic", "nictype": "bridged"},
			new:          Device{"type": "nic", "nictype": "bridged"},
			expectedKeys: nil,
		},
		{
			name:         "BothEmpty",
			old:          Device{},
			new:          Device{},
			expectedKeys: nil,
		},
		{
			name:         "ChangedValue",
			old:          Device{"type": "nic", "nictype": "bridged"},
			new:          Device{"type": "nic", "nictype": "macvlan"},
			expectedKeys: []string{"nictype"},
		},
		{
			name:         "AddedKey",
			old:          Device{"type": "nic"},
			new:          Device{"type": "nic", "nictype": "bridged"},
			expectedKeys: []string{"nictype"},
		},
		{
			name:         "RemovedKey",
			old:          Device{"type": "nic", "nictype": "bridged"},
			new:          Device{"type": "nic"},
			expectedKeys: []string{"nictype"},
		},
		{
			name:         "MultipleChanges",
			old:          Device{"type": "nic", "nictype": "bridged", "parent": "eth0"},
			new:          Device{"type": "nic", "nictype": "macvlan", "mtu": "1500"},
			expectedKeys: []string{"nictype", "mtu", "parent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deviceEqualsDiffKeys(tt.old, tt.new)
			assert.ElementsMatch(t, tt.expectedKeys, result)
		})
	}
}
