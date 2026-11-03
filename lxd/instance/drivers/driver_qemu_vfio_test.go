package drivers

import (
	"testing"

	"github.com/canonical/lxd/lxd/instance/drivers/qmp"
)

func TestQemuVFIODeviceAddTimeout(t *testing.T) {
	t.Run("default when no memory limit is set", func(t *testing.T) {
		d := &qemu{}

		got := d.vfioDeviceAddTimeout()
		if got != qmp.DeviceAddTimeout(0) {
			t.Fatalf("unexpected VFIO device add timeout:\n- want: %v\n-  got: %v", qmp.DeviceAddTimeout(0), got)
		}
	})

	t.Run("scales with the instance memory limit", func(t *testing.T) {
		d := &qemu{common: common{expandedConfig: map[string]string{"limits.memory": "4GiB"}}}

		want := qmp.DeviceAddTimeout(4 * 1 << 30)
		got := d.vfioDeviceAddTimeout()
		if got != want {
			t.Fatalf("unexpected VFIO device add timeout for a 4GiB instance:\n- want: %v\n-  got: %v", want, got)
		}
	})

	t.Run("caps the timeout for large instances", func(t *testing.T) {
		d := &qemu{common: common{expandedConfig: map[string]string{"limits.memory": "244GiB"}}}

		want := qmp.DeviceAddTimeout(244 * 1 << 30)
		got := d.vfioDeviceAddTimeout()
		if got != want {
			t.Fatalf("unexpected capped VFIO device add timeout for a 244GiB instance:\n- want: %v\n-  got: %v", want, got)
		}
	})

	t.Run("honors hugepages configuration", func(t *testing.T) {
		d := &qemu{common: common{expandedConfig: map[string]string{
			"limits.memory":           "244GiB",
			"limits.memory.hugepages": "true",
		}}}

		got := d.vfioDeviceAddTimeout()
		if got != qmp.DeviceAddTimeout(0) {
			t.Fatalf("unexpected VFIO device add timeout with hugepages enabled:\n- want: %v\n-  got: %v", qmp.DeviceAddTimeout(0), got)
		}
	})
}
