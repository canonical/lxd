package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiskQueuesToRecord(t *testing.T) {
	tests := []struct {
		name       string
		stateful   bool
		recorded   string
		wantValue  string
		wantUpdate bool
	}{
		{"fresh start records the current value", false, "", "4", true},
		{"restart of a legacy VM upgrades it", false, "", "4", true},
		{"restart with the current value does not write", false, "4", "4", false},
		{"restart replaces an outdated value", false, "2", "4", true},
		{"stateful start keeps the recorded value", true, "2", "2", false},
		{"stateful start of a legacy VM stays legacy", true, "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, update := diskQueuesToRecord(tt.stateful, tt.recorded)
			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantUpdate, update)
		})
	}
}
