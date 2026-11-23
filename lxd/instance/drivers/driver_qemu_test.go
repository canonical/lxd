package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/migration"
	"github.com/canonical/lxd/shared/api"
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

func TestSnapshotMigrationInstance(t *testing.T) {
	both := map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}

	tests := []struct {
		name     string
		snap     *api.InstanceSnapshot
		live     bool
		current  map[string]string
		wantFlag bool
		want     map[string]string
	}{
		{
			"stateful snapshot gives its keys to the target",
			&api.InstanceSnapshot{Name: "snap1", Stateful: true, Config: both},
			false, map[string]string{}, true,
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"stateless snapshot gives no keys to the target",
			&api.InstanceSnapshot{Name: "snap0", Stateful: false, Config: both},
			false, both, false,
			map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""},
		},
		{
			"stateful snapshot of a legacy VM gives no keys",
			&api.InstanceSnapshot{Name: "snap1", Stateful: true, Config: map[string]string{"limits.cpu": "2"}},
			false, both, true,
			map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""},
		},
		{
			"stateful snapshot with an invalid value is not taken",
			&api.InstanceSnapshot{Name: "snap1", Stateful: true, Config: map[string]string{"volatile.disk_queues": "999", "volatile.disk_iothreads": "1"}},
			false, map[string]string{}, true,
			map[string]string{"volatile.disk_iothreads": "1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := snapshotMigrationInstance(tt.snap)
			assert.Equal(t, tt.snap.Name, inst.Name)
			assert.Equal(t, tt.wantFlag, inst.Stateful)

			info := &migration.Info{Config: &backupConfig.Config{Instance: inst}}
			assert.Equal(t, tt.want, diskPropsToApply(info, tt.live, tt.current))
		})
	}
}

// TestDiskPropsIndependentOfCPU checks that the recorded disk properties do not depend on the number of vCPUs.
// QEMU needs identical device properties on both ends of a live migration and on a resume, and limits.cpu can
// change in between.
func TestDiskPropsIndependentOfCPU(t *testing.T) {
	both := map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}

	for _, cpus := range []string{"", "1", "2", "4", "16", "0-3", "64"} {
		t.Run("limits.cpu="+cpus, func(t *testing.T) {
			// A fresh start records the same values whatever the CPU limit is.
			fresh := map[string]string{"limits.cpu": cpus}
			assert.Equal(t, both, diskPropsToRecord(false, fresh))

			// A start with the recorded values does not write anything after the CPU limit changed.
			recorded := map[string]string{"limits.cpu": cpus, "volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}
			assert.Empty(t, diskPropsToRecord(false, recorded))

			// A stateful start never records, so the values of the first boot are reused.
			assert.Empty(t, diskPropsToRecord(true, recorded))
			assert.Empty(t, diskPropsToRecord(true, fresh))

			// The properties used for the devices are the recorded ones.
			props := qemuDiskPropsFromConfig(recorded)
			assert.Equal(t, 4, props.Queues)
			assert.Equal(t, 1, props.IOThreads)
		})
	}
}

func TestDiskPropsToApply(t *testing.T) {
	sourceInfo := func(stateful bool, config map[string]string) *migration.Info {
		return &migration.Info{Config: &backupConfig.Config{Instance: &api.Instance{
			Config:   config,
			Stateful: stateful,
		}}}
	}

	both := map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"}

	tests := []struct {
		name   string
		info   *migration.Info
		live   bool
		config map[string]string
		want   map[string]string
	}{
		{
			"live migration takes both keys from the source",
			sourceInfo(false, both), true, map[string]string{},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"live migration replaces the keys of the request",
			sourceInfo(false, both), true, map[string]string{"volatile.disk_queues": "64", "volatile.disk_iothreads": "0"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"live migration with matching keys changes nothing",
			sourceInfo(false, both), true, both,
			map[string]string{},
		},
		{
			"stopped source with a saved state takes both keys",
			sourceInfo(true, both), false, map[string]string{},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"stopped source without a saved state drops the keys of the request",
			sourceInfo(false, both), false, both,
			map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""},
		},
		{
			"stopped source without a saved state and without keys changes nothing",
			sourceInfo(false, map[string]string{}), false, map[string]string{},
			map[string]string{},
		},
		{
			"live migration of a source without keys removes the keys of the request",
			sourceInfo(false, map[string]string{"limits.cpu": "2"}), true, both,
			map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""},
		},
		{
			"live migration takes only the key the source has",
			sourceInfo(false, map[string]string{"volatile.disk_queues": "4"}), true, both,
			map[string]string{"volatile.disk_iothreads": ""},
		},
		{
			"no index header of an older source leaves the target without keys",
			nil, true, both,
			map[string]string{"volatile.disk_queues": "", "volatile.disk_iothreads": ""},
		},
		{
			"index header without instance record leaves the target without keys",
			&migration.Info{}, true, map[string]string{},
			map[string]string{},
		},
		{
			"invalid value of the source is not taken",
			sourceInfo(false, map[string]string{"volatile.disk_queues": "999", "volatile.disk_iothreads": "1"}), true, map[string]string{},
			map[string]string{"volatile.disk_iothreads": "1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, diskPropsToApply(tt.info, tt.live, tt.config))
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
