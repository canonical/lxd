package api

import (
	"testing"
)

func TestApplyRefreshConfig(t *testing.T) {
	target := Instance{
		Config: map[string]string{
			"volatile.idmap.next":       "target-idmap",
			"volatile.last_state.power": "RUNNING",
		},
		Devices: map[string]map[string]string{
			"root": {
				"type": "disk",
				"path": "/",
				"pool": "target-pool",
			},
		},
	}

	instance := InstancePut{
		Config: map[string]string{
			"user.foo":                  "bar",
			"volatile.idmap.next":       "source-idmap",
			"volatile.last_state.power": "STOPPED",
		},
		Devices: map[string]map[string]string{
			"root": {
				"type": "disk",
				"path": "/",
				"pool": "source-pool",
			},
		},
	}

	instance.ApplyRefreshConfig(target)

	if instance.Config["user.foo"] != "bar" {
		t.Fatalf("Expected user config value to be preserved, got %q", instance.Config["user.foo"])
	}

	if instance.Config["volatile.idmap.next"] != "target-idmap" {
		t.Fatalf("Expected volatile.idmap.next to be preserved from target, got %q", instance.Config["volatile.idmap.next"])
	}

	if instance.Config["volatile.last_state.power"] != "RUNNING" {
		t.Fatalf("Expected volatile.last_state.power to be preserved from target, got %q", instance.Config["volatile.last_state.power"])
	}

	if instance.Devices["root"]["pool"] != "target-pool" {
		t.Fatalf("Expected root pool to be preserved from target, got %q", instance.Devices["root"]["pool"])
	}
}

// TestApplyRefreshConfigDiskProps checks that a refresh keeps the disk property keys of the target, for each
// key on its own and for both together, whatever the source says.
func TestApplyRefreshConfigDiskProps(t *testing.T) {
	tests := []struct {
		name   string
		target map[string]string
		source map[string]string
		want   map[string]string
	}{
		{
			"target values replace the source values",
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			map[string]string{"volatile.disk_queues": "2", "volatile.disk_iothreads": "0"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
		},
		{
			"target queues replace the source queues",
			map[string]string{"volatile.disk_queues": "4"},
			map[string]string{"volatile.disk_queues": "2"},
			map[string]string{"volatile.disk_queues": "4"},
		},
		{
			"target iothreads replace the source iothreads",
			map[string]string{"volatile.disk_iothreads": "1"},
			map[string]string{"volatile.disk_iothreads": "0"},
			map[string]string{"volatile.disk_iothreads": "1"},
		},
		{
			"target without the keys removes both source keys",
			map[string]string{},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			map[string]string{},
		},
		{
			"target with one key removes the other source key",
			map[string]string{"volatile.disk_queues": "4"},
			map[string]string{"volatile.disk_queues": "2", "volatile.disk_iothreads": "1"},
			map[string]string{"volatile.disk_queues": "4"},
		},
		{
			"source without the keys gets the target keys",
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1"},
			map[string]string{"user.foo": "bar"},
			map[string]string{"volatile.disk_queues": "4", "volatile.disk_iothreads": "1", "user.foo": "bar"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := InstancePut{Config: tt.source}
			instance.ApplyRefreshConfig(Instance{Config: tt.target})

			if len(instance.Config) != len(tt.want) {
				t.Fatalf("Expected config %v, got %v", tt.want, instance.Config)
			}

			for key, value := range tt.want {
				if instance.Config[key] != value {
					t.Fatalf("Expected %s=%q, got %q (config %v)", key, value, instance.Config[key], instance.Config)
				}
			}
		})
	}
}

func TestApplyInstanceRefreshConfigMissingTargetKey(t *testing.T) {
	target := Instance{
		Config: map[string]string{},
	}

	instance := InstancePut{
		Config: map[string]string{
			"volatile.idmap.next":       "source-idmap",
			"volatile.last_state.power": "STOPPED",
		},
	}

	instance.ApplyRefreshConfig(target)

	_, found := instance.Config["volatile.idmap.next"]
	if found {
		t.Fatalf("Expected volatile.idmap.next to be removed when missing in target")
	}

	_, found = instance.Config["volatile.last_state.power"]
	if found {
		t.Fatalf("Expected volatile.last_state.power to be removed when missing in target")
	}
}

func TestApplyInstanceRefreshConfigMismatchedRootDeviceKey(t *testing.T) {
	target := Instance{
		Devices: map[string]map[string]string{
			"root-target": {
				"type": "disk",
				"path": "/",
				"pool": "target-pool",
			},
		},
	}

	instance := InstancePut{
		Devices: map[string]map[string]string{
			"root-source": {
				"type": "disk",
				"path": "/",
				"pool": "source-pool",
			},
		},
	}

	instance.ApplyRefreshConfig(target)

	if instance.Devices["root-source"]["pool"] != "source-pool" {
		t.Fatalf("Expected root pool to remain unchanged when root device keys differ, got %q", instance.Devices["root-source"]["pool"])
	}
}
