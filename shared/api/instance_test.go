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

func TestInstanceCreateConfigKeyPolicyRemovesPeerLinkID(t *testing.T) {
	config := map[string]string{
		"user.foo":                   "bar",
		"volatile.eth1.peer.link_id": "link-id",
		"volatile.eth1.host_name":    "veth1234",
		"volatile.uuid":              "uuid",
	}

	InstanceCreateConfigKeyPolicy.Apply(config, nil)

	_, found := config["volatile.eth1.peer.link_id"]
	if found {
		t.Fatalf("Expected volatile.eth1.peer.link_id to be removed")
	}

	if config["volatile.eth1.host_name"] != "veth1234" {
		t.Fatalf("Expected volatile.eth1.host_name to be preserved, got %q", config["volatile.eth1.host_name"])
	}

	if config["volatile.uuid"] != "uuid" {
		t.Fatalf("Expected volatile.uuid to be preserved, got %q", config["volatile.uuid"])
	}

	if config["user.foo"] != "bar" {
		t.Fatalf("Expected user config value to be preserved, got %q", config["user.foo"])
	}
}

func TestApplyRefreshConfigRemovesPeerLinkID(t *testing.T) {
	target := Instance{
		Config: map[string]string{
			"volatile.idmap.next": "target-idmap",
		},
	}

	instance := InstancePut{
		Config: map[string]string{
			"user.foo":                   "bar",
			"volatile.eth1.peer.link_id": "source-link-id",
			"volatile.idmap.next":        "source-idmap",
		},
	}

	instance.ApplyRefreshConfig(target)

	_, found := instance.Config["volatile.eth1.peer.link_id"]
	if found {
		t.Fatalf("Expected volatile.eth1.peer.link_id to be removed by refresh")
	}

	if instance.Config["volatile.idmap.next"] != "target-idmap" {
		t.Fatalf("Expected volatile.idmap.next to be preserved from target, got %q", instance.Config["volatile.idmap.next"])
	}

	if instance.Config["user.foo"] != "bar" {
		t.Fatalf("Expected user config value to be preserved, got %q", instance.Config["user.foo"])
	}
}
