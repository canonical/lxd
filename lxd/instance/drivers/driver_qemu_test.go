package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiskPropsToRecord(t *testing.T) {
	tests := []struct {
		name     string
		stateful bool
		config   map[string]string
		want     map[string]string
	}{
		{
			"fresh start records both keys",
			false,
			map[string]string{},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"restart of a VM without recorded properties records both keys",
			false,
			map[string]string{"limits.cpu": "2"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"restart with the current values does not write",
			false,
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			map[string]string{},
		},
		{
			"restart replaces an outdated queue count only",
			false,
			map[string]string{"volatile.disk_queues": "2", "volatile.disk_iothreads": "1"},
			map[string]string{"volatile.disk_queues": "4"},
		},
		{
			"restart writes the key that is missing",
			false,
			map[string]string{"volatile.disk_queues": "4"},
			map[string]string{"volatile.disk_iothreads": "1"},
		},
		{
			"stateful start keeps the recorded values",
			true,
			map[string]string{"volatile.disk_queues": "2", "volatile.disk_iothreads": "0"},
			nil,
		},
		{
			"stateful start of a VM without recorded properties stays so",
			true,
			map[string]string{},
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diskPropsToRecord(tt.stateful, tt.config)
			assert.Len(t, got, len(tt.want))
			for key, value := range tt.want {
				assert.Equal(t, value, got[key], key)
			}
		})
	}
}

func TestApplyVolatileInMemory(t *testing.T) {
	tests := []struct {
		name    string
		changes map[string]string
		local   map[string]string
		wantSet map[string]string
	}{
		{
			name:    "both keys are added to a VM without recorded properties",
			changes: map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			local:   map[string]string{"limits.cpu": "2"},
			wantSet: map[string]string{"limits.cpu": "2", "volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			name:    "only the queue key is replaced",
			changes: map[string]string{"volatile.disk_queues": "4"},
			local:   map[string]string{"volatile.disk_queues": "2", "volatile.disk_iothreads": "1"},
			wantSet: map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			name:    "only the iothreads key is added",
			changes: map[string]string{"volatile.disk_iothreads": "1"},
			local:   map[string]string{"volatile.disk_queues": "4"},
			wantSet: map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			name:    "an empty value removes the key",
			changes: map[string]string{"volatile.disk_queues": ""},
			local:   map[string]string{"volatile.disk_queues": "4"},
			wantSet: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := map[string]string{}
			expanded := map[string]string{"profile": "kept"}
			for k, v := range tt.local {
				local[k] = v
				expanded[k] = v
			}

			original := map[string]string{}
			for k, v := range tt.local {
				original[k] = v
			}

			originalExpanded := map[string]string{}
			for k, v := range expanded {
				originalExpanded[k] = v
			}

			undo := applyVolatileInMemory(tt.changes, local, expanded)

			// Both configs get the changes.
			assert.Equal(t, tt.wantSet, local)
			for k, v := range tt.wantSet {
				assert.Equal(t, v, expanded[k], k)
			}

			assert.Equal(t, "kept", expanded["profile"])

			// Undo restores the previous values and removes the keys that were not there.
			undo()
			assert.Equal(t, original, local)
			assert.Equal(t, originalExpanded, expanded)
		})
	}
}

func TestQemuDiskPropsFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]string
		want   qemuDiskProps
	}{
		{"not recorded", map[string]string{}, qemuDiskProps{0, 0}},
		{"both recorded", map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}, qemuDiskProps{4, 1}},
		{"queues only", map[string]string{"volatile.disk_queues": "4"}, qemuDiskProps{4, 0}},
		{"iothreads only", map[string]string{"volatile.disk_iothreads": "1"}, qemuDiskProps{0, 1}},
		{"recorded as zero", map[string]string{"volatile.disk_queues": "0", "volatile.disk_iothreads": "0"}, qemuDiskProps{0, 0}},
		{"invalid", map[string]string{"volatile.disk_queues": "many", "volatile.disk_iothreads": "yes"}, qemuDiskProps{0, 0}},
		{"negative", map[string]string{"volatile.disk_queues": "-2", "volatile.disk_iothreads": "-1"}, qemuDiskProps{0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, qemuDiskPropsFromConfig(tt.config))
		})
	}
}
