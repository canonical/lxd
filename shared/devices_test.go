package shared

import (
	"reflect"
	"testing"
)

func TestSortableDevices(t *testing.T) {
	devices := Devices{
		"1": Device{"type": "nic"},
		"3": Device{"type": "disk", "path": "/foo/bar"},
		"4": Device{"type": "disk", "path": "/foo"},
		"2": Device{"type": "nic"},
	}

	expected := []string{"1", "2", "4", "3"}

	result := devices.DeviceNames()
	if !reflect.DeepEqual(result, expected) {
		t.Error("devices sorted incorrectly")
	}
}
