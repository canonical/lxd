package main

import (
	"fmt"
	"testing"

	"github.com/lxc/lxd/shared"
)

func Test_disk_device_returns_simple_mount_entry(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "disk"
	device["path"] = "home/someguy"
	device["source"] = "/home/someguy"

	result, _ := DeviceToLxc(device)
	unwrapped := result[0]

	expected := []string{"lxc.mount.entry", "/home/someguy home/someguy none bind,create=file 0 0"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Error(fmt.Sprintf("Expected '%s', got '%s' instead!", expected, unwrapped))
		}
	}
}

func Test_disk_device_returns_readonly_mount_entry(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "disk"
	device["path"] = "home/someguy"
	device["source"] = "/home/someguy"
	device["readonly"] = "true"

	result, _ := DeviceToLxc(device)
	unwrapped := result[0]

	expected := []string{"lxc.mount.entry", "/home/someguy home/someguy none bind,create=file,ro 0 0"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Error(fmt.Sprintf("Expected '%s', got '%s' instead!", expected, unwrapped))
		}
	}
}

func Test_disk_device_returns_optional_mount_entry(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "disk"
	device["path"] = "home/someguy"
	device["source"] = "/home/someguy"
	device["optional"] = "true"

	result, _ := DeviceToLxc(device)
	unwrapped := result[0]

	expected := []string{"lxc.mount.entry", "/home/someguy home/someguy none bind,create=file,optional 0 0"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Error(fmt.Sprintf("Expected '%s', got '%s' instead!", expected, unwrapped))
		}
	}
}

func Test_none_device_returns_nil(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "none"

	result, _ := DeviceToLxc(device)
	if result != nil {
		t.Error("'none' device type should return nil.")
	}
}

func Test_nic_device_returns_config_line(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "nic"
	device["nictype"] = "bridged"

	result, _ := DeviceToLxc(device)
	unwrapped := result[0]

	expected := []string{"lxc.network.type", "veth"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Error(fmt.Sprintf("Expected '%s', got '%s' instead!", expected, unwrapped))
		}
	}
}
