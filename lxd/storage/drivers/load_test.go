package drivers

import (
	"testing"
)

func TestDefaultVMBlockFilesystemSize(t *testing.T) {
	for driverName := range drivers {
		size, err := DefaultVMBlockFilesystemSize(driverName)
		if err != nil {
			t.Errorf("Failed to get DefaultVMBlockFilesystemSize for storage driver %q: %s", driverName, err)
		}

		if size == "" {
			t.Errorf("Missing DefaultVMBlockFilesystemSize for storage driver %q", driverName)
		}
	}
}
