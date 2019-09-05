package config

import (
	"reflect"
	"testing"
)

func TestSortableDevices(t *testing.T) {
	devices := Devices{
		"dev1": Device{"type": "nic"},
		"dev3": Device{"type": "disk", "path": "/foo/bar"},
		"dev4": Device{"type": "disk", "path": "/foo"},
		"dev2": Device{"type": "nic"},
	}

	expectedSorted := DevicesSortable{
		DeviceNamed{Name: "dev4", Config: Device{"type": "disk", "path": "/foo"}},
		DeviceNamed{Name: "dev3", Config: Device{"type": "disk", "path": "/foo/bar"}},
		DeviceNamed{Name: "dev1", Config: Device{"type": "nic"}},
		DeviceNamed{Name: "dev2", Config: Device{"type": "nic"}},
	}

	result := devices.Sorted()
	if !reflect.DeepEqual(result, expectedSorted) {
		t.Error("devices sorted incorrectly")
	}

	expectedReversed := DevicesSortable{
		DeviceNamed{Name: "dev2", Config: Device{"type": "nic"}},
		DeviceNamed{Name: "dev1", Config: Device{"type": "nic"}},
		DeviceNamed{Name: "dev3", Config: Device{"type": "disk", "path": "/foo/bar"}},
		DeviceNamed{Name: "dev4", Config: Device{"type": "disk", "path": "/foo"}},
	}

	result = devices.Reversed()
	if !reflect.DeepEqual(result, expectedReversed) {
		t.Error("devices reverse sorted incorrectly")
	}
}
