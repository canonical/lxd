package cgroup

import (
	"fmt"
)

// CGroup represents the main cgroup abstraction.
type CGroup struct {
	rw ReadWriter
}

// SetMaxProcesses applies a limit to the number of processes
func (cg *CGroup) SetMaxProcesses(max int64) error {
	// Confirm we have the controller
	version := cgControllers["pids"]
	if version == Unavailable {
		return ErrControllerMissing
	}

	// V1/V2 behavior
	if version == V1 || version == V2 {
		// Setting pids limits is conveniently identical on V1 and V2.
		if max == -1 {
			return cg.rw.Set(version, "pids", "pids.max", "max")
		}

		return cg.rw.Set(version, "pids", "pids.max", fmt.Sprintf("%d", max))
	}

	return ErrUnknownVersion
}
