package main

import (
	"strings"
	"testing"

	"github.com/lxc/lxd/shared"
)

func Test_disk_device_returns_simple_mount_entry(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "disk"
	device["path"] = "home/someguy"
	device["source"] = "/home/someguy"

	result, _ := deviceToLxc("", device)
	unwrapped := result[0]

	expected := []string{"lxc.mount.entry", "/home/someguy home/someguy none bind,create=file 0 0"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Errorf("Expected '%s', got '%s' instead!", expected, unwrapped)
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

	result, _ := deviceToLxc("", device)
	unwrapped := result[0]

	expected := []string{"lxc.mount.entry", "/home/someguy home/someguy none bind,create=file,ro 0 0"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Errorf("Expected '%s', got '%s' instead!", expected, unwrapped)
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

	result, _ := deviceToLxc("", device)
	unwrapped := result[0]

	expected := []string{"lxc.mount.entry", "/home/someguy home/someguy none bind,create=file,optional 0 0"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Errorf("Expected '%s', got '%s' instead!", expected, unwrapped)
		}
	}
}

func Test_none_device_returns_nil(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "none"

	result, _ := deviceToLxc("", device)
	if result != nil {
		t.Error("'none' device type should return nil.")
	}
}

func Test_nic_device_returns_config_line(t *testing.T) {
	var device shared.Device
	device = make(shared.Device)

	device["type"] = "nic"
	device["nictype"] = "bridged"
	device["parent"] = "lxcbr0"

	result, _ := deviceToLxc("", device)
	unwrapped := result[0]

	expected := []string{"lxc.network.type", "veth"}

	for key := range unwrapped {
		if unwrapped[key] != expected[key] {
			t.Errorf("Expected '%s', got '%s' instead!", expected, unwrapped)
		}
	}
}

func devModeContains(str1, str2 string) bool {
	for _, c := range str1 {
		if !strings.Contains(str2, string(c)) {
			return false
		}
	}
	return true
}

func devModeEquiv(str1, str2 string) bool {
	if devModeContains(str1, str2) && devModeContains(str2, str1) {
		return true
	}
	return false
}

func Test_dev_mode_parse(t *testing.T) {
	tests := [][]string{{"", "rwm"}, {"0660", "rwm"}, {"0040", "rm"}, {"0002", "wm"}}
	for _, arr := range tests {
		key := arr[0]
		answer := arr[1]
		reply, err := devModeString(key)
		if err != nil {
			t.Errorf("Unexpected unix mode parse failure: %s", err)
		}
		if !devModeEquiv(reply, answer) {
			t.Errorf("Wrong unix mode result: Got %s expected %s", reply, answer)
		}
	}
}
