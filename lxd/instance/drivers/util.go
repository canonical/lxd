package drivers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/units"
)

// parseMemoryStr parses a human-readable representation of a memory value.
func parseMemoryStr(memory string) (valueInt int64, err error) {
	if strings.HasSuffix(memory, "%") {
		var percent, memoryTotal int64

		percent, err = strconv.ParseInt(strings.TrimSuffix(memory, "%"), 10, 64)
		if err != nil {
			return 0, err
		}

		memoryTotal, err = shared.DeviceTotalMemory()
		if err != nil {
			return 0, err
		}

		valueInt = (memoryTotal / 100) * percent
	} else {
		valueInt, err = units.ParseByteSizeString(memory)
	}

	return valueInt, err
}

// validateMemoryLimit checks if the memory limit is too low (less than 50MiB).
func validateMemoryLimit(valueInt int64) error {
	if valueInt < 50*1024*1024 {
		return fmt.Errorf("memory limit is too low (minimum 50MiB). Did you mean GiB?")
	}

	return nil
}
