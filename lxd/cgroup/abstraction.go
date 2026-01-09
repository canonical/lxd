package cgroup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared"
)

// CGroup represents the main cgroup abstraction.
type CGroup struct {
	rw             ReadWriter
	UnifiedCapable bool
}

// SetMaxProcesses applies a limit to the number of processes.
func (cg *CGroup) SetMaxProcesses(limit int64) error {
	version := cgControllers["pids"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1, V2:
		if limit == -1 {
			return cg.rw.Set(version, "pids", "pids.max", "max")
		}

		return cg.rw.Set(version, "pids", "pids.max", strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// GetMemorySoftLimit returns the soft limit for memory.
func (cg *CGroup) GetMemorySoftLimit() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.soft_limit_in_bytes")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.high")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	return -1, ErrUnknownVersion
}

// SetMemorySoftLimit set the soft limit for memory.
func (cg *CGroup) SetMemorySoftLimit(limit int64) error {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "memory", "memory.soft_limit_in_bytes", strconv.FormatInt(limit, 10))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "memory", "memory.high", "max")
		}

		return cg.rw.Set(version, "memory", "memory.high", strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// GetMemoryLimit return the hard limit for memory.
func (cg *CGroup) GetMemoryLimit() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.limit_in_bytes")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.max")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	return -1, ErrUnknownVersion
}

// GetEffectiveMemoryLimit return the effective hard limit for memory.
// Returns the cgroup memory limit, or if the cgroup memory limit couldn't be determined or is larger than the
// total system memory, then the total system memory is returned.
func (cg *CGroup) GetEffectiveMemoryLimit() (int64, error) {
	memoryTotal, err := shared.DeviceTotalMemory()
	if err != nil {
		return -1, fmt.Errorf("Failed getting total memory: %q", err)
	}

	memoryLimit, err := cg.GetMemoryLimit()
	if err != nil || memoryLimit > memoryTotal {
		return memoryTotal, nil
	}

	return memoryLimit, nil
}

// SetMemoryLimit sets the hard limit for memory.
func (cg *CGroup) SetMemoryLimit(limit int64) error {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "memory", "memory.limit_in_bytes", strconv.FormatInt(limit, 10))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "memory", "memory.max", "max")
		}

		return cg.rw.Set(version, "memory", "memory.max", strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// GetMemoryUsage returns the current use of memory.
func (cg *CGroup) GetMemoryUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.usage_in_bytes")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.current")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	return -1, ErrUnknownVersion
}

// GetProcessesUsage returns the current number of pids.
func (cg *CGroup) GetProcessesUsage() (int64, error) {
	version := cgControllers["pids"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1, V2:
		val, err := cg.rw.Get(version, "pids", "pids.current")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	return -1, ErrUnknownVersion
}

// SetMemorySwapLimit sets the hard limit for swap.
func (cg *CGroup) SetMemorySwapLimit(limit int64) error {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		if limit == -1 {
			return cg.rw.Set(version, "memory", "memory.memsw.limit_in_bytes", "-1")
		}

		val, err := cg.rw.Get(version, "memory", "memory.limit_in_bytes")
		if err != nil {
			return err
		}

		valInt, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return cg.rw.Set(version, "memory", "memory.memsw.limit_in_bytes", strconv.FormatInt(limit+valInt, 10))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "memory", "memory.swap.max", "max")
		}

		return cg.rw.Set(version, "memory", "memory.swap.max", strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// GetCPUAcctUsageAll returns the user and system CPU times of each CPU thread in ns used by processes.
func (cg *CGroup) GetCPUAcctUsageAll() (map[int64]CPUStats, error) {
	out := map[int64]CPUStats{}

	version := cgControllers["cpuacct"]
	switch version {
	case V1:
		val, err := cg.rw.Get(version, "cpuacct", "cpuacct.usage_all")
		if err != nil {
			return nil, err
		}

		scanner := bufio.NewScanner(strings.NewReader(val))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			// Skip header
			if fields[0] == "cpu" {
				continue
			}

			stats := CPUStats{}

			cpuID, err := strconv.ParseInt(fields[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q: %w", fields[0], err)
			}

			stats.User, err = strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q: %w", fields[0], err)
			}

			stats.System, err = strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q: %w", fields[0], err)
			}

			out[cpuID] = stats
		}

		return out, nil
	}

	// Handle cgroups v2
	version = cgControllers["cpu"]
	switch version {
	case Unavailable:
		return nil, ErrControllerMissing
	case V2:
		val, err := cg.rw.Get(version, "cpu", "cpu.stat")
		if err != nil {
			return nil, err
		}

		stats := CPUStats{}

		scanner := bufio.NewScanner(strings.NewReader(val))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			switch fields[0] {
			case "user_usec":
				val, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("Failed parsing %q: %w", val, err)
				}

				// Convert usec to nsec
				stats.User = val * 1000
			case "system_usec":
				val, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("Failed parsing %q: %w", val, err)
				}

				// Convert usec to nsec
				stats.System = val * 1000
			}
		}

		// Use CPU ID 0 here as cgroup v2 doesn't show the usage of separate CPUs.
		out[0] = stats

		return out, nil
	}

	return nil, ErrUnknownVersion
}

// GetCPUAcctUsage returns the total CPU time in ns used by processes.
func (cg *CGroup) GetCPUAcctUsage() (int64, error) {
	version := cgControllers["cpuacct"]
	switch version {
	case V1:
		val, err := cg.rw.Get(version, "cpuacct", "cpuacct.usage")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	// Handle cgroups v2
	version = cgControllers["cpu"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V2:
		stats, err := cg.rw.Get(version, "cpu", "cpu.stat")
		if err != nil {
			return -1, err
		}

		scanner := bufio.NewScanner(strings.NewReader(stats))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if fields[0] != "usage_usec" {
				continue
			}

			val, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
			}

			// Convert usec to nsec
			return val * 1000, nil
		}
	}

	return -1, ErrUnknownVersion
}

// GetEffectiveCPUs returns the total number of effective CPUs.
func (cg *CGroup) GetEffectiveCPUs() (int, error) {
	set, err := cg.GetEffectiveCpuset()
	if err != nil {
		return -1, err
	}

	return parseCPUSet(set)
}

// parseCPUSet parses a cpuset string and returns the number of CPUs.
func parseCPUSet(set string) (int, error) {
	var out int

	fields := strings.SplitSeq(strings.TrimSpace(set), ",")
	for value := range fields {
		startStr, endStr, found := strings.Cut(value, "-")
		if found {
			// Parse ranges.
			startRange, err := strconv.Atoi(startStr)
			if err != nil {
				return -1, fmt.Errorf("Failed parsing %q: %w", startStr, err)
			}

			endRange, err := strconv.Atoi(endStr)
			if err != nil {
				return -1, fmt.Errorf("Failed parsing %q: %w", endStr, err)
			}

			for i := startRange; i <= endRange; i++ {
				out++
			}
		} else {
			// Parse non-range values.
			_, err := strconv.Atoi(value)
			if err != nil {
				return -1, fmt.Errorf("Failed parsing %q: %w", value, err)
			}

			out++
		}
	}

	if out == 0 {
		return -1, fmt.Errorf("Failed parsing %q", set)
	}

	return out, nil
}

// GetMemoryMaxUsage returns the record high for memory usage.
func (cg *CGroup) GetMemoryMaxUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.max_usage_in_bytes")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	case V2:
		return -1, ErrControllerMissing
	}

	return -1, ErrUnknownVersion
}

// GetMemorySwapMaxUsage returns the record high for swap usage.
func (cg *CGroup) GetMemorySwapMaxUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		swapVal, err := cg.rw.Get(version, "memory", "memory.memsw.max_usage_in_bytes")
		if err != nil {
			return -1, err
		}

		swapValInt, err := strconv.ParseInt(swapVal, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", swapVal, err)
		}

		memVal, err := cg.rw.Get(version, "memory", "memory.max_usage_in_bytes")
		if err != nil {
			return -1, err
		}

		memValInt, err := strconv.ParseInt(memVal, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", memVal, err)
		}

		return swapValInt - memValInt, nil
	case V2:
		return -1, ErrControllerMissing
	}

	return -1, ErrUnknownVersion
}

// SetMemorySwappiness sets swappiness paramet of vmscan.
func (cg *CGroup) SetMemorySwappiness(limit int64) error {
	// Confirm we have the controller
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "memory", "memory.swappiness", strconv.FormatInt(limit, 10))
	case V2:
		return ErrControllerMissing
	}

	return ErrUnknownVersion
}

// GetMemorySwapLimit returns the hard limit on swap usage.
func (cg *CGroup) GetMemorySwapLimit() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		swapVal, err := cg.rw.Get(version, "memory", "memory.memsw.limit_in_bytes")
		if err != nil {
			return -1, err
		}

		swapValInt, err := strconv.ParseInt(swapVal, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", swapVal, err)
		}

		memVal, err := cg.rw.Get(version, "memory", "memory.limit_in_bytes")
		if err != nil {
			return -1, err
		}

		memValInt, err := strconv.ParseInt(memVal, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", memVal, err)
		}

		return swapValInt - memValInt, nil
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.swap.max")
		if err != nil {
			return -1, err
		}

		if val == "max" {
			return shared.GetMeminfo("SwapTotal")
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	return -1, ErrUnknownVersion
}

// GetMemorySwapUsage return current usage of swap.
func (cg *CGroup) GetMemorySwapUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		swapVal, err := cg.rw.Get(version, "memory", "memory.memsw.usage_in_bytes")
		if err != nil {
			return -1, err
		}

		swapValInt, err := strconv.ParseInt(swapVal, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", swapVal, err)
		}

		memVal, err := cg.rw.Get(version, "memory", "memory.usage_in_bytes")
		if err != nil {
			return -1, err
		}

		memValInt, err := strconv.ParseInt(memVal, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", memVal, err)
		}

		return swapValInt - memValInt, nil
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.swap.current")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	return -1, ErrUnknownVersion
}

// GetBlkioWeight returns the currently allowed range of weights.
func (cg *CGroup) GetBlkioWeight() (int64, error) {
	version := cgControllers["blkio"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "blkio", "blkio.weight")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	case V2:
		val, err := cg.rw.Get(version, "io", "io.weight")
		if err != nil {
			return -1, err
		}

		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return -1, fmt.Errorf("Failed parsing %q: %w", val, err)
		}

		return n, nil
	}

	return -1, ErrUnknownVersion
}

// SetBlkioWeight sets the currently allowed range of weights.
func (cg *CGroup) SetBlkioWeight(limit int64) error {
	version := cgControllers["blkio"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "blkio", "blkio.weight", strconv.FormatInt(limit, 10))
	case V2:
		return cg.rw.Set(version, "io", "io.weight", strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// SetBlkioLimit sets the specified read or write limit for a device.
func (cg *CGroup) SetBlkioLimit(dev string, oType string, uType string, limit int64) error {
	if oType != "read" && oType != "write" {
		return fmt.Errorf("Invalid I/O operation type: %s", oType)
	}

	if uType != "iops" && uType != "bps" {
		return fmt.Errorf("Invalid I/O limit type: %s", uType)
	}

	version := cgControllers["blkio"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "blkio", "blkio.throttle."+oType+"_"+uType+"_device", dev+strconv.FormatInt(limit, 10))
	case V2:
		var op string
		switch oType {
		case "read":
			op = "r" + uType
		case "write":
			op = "w" + uType
		}

		return cg.rw.Set(version, "io", "io.max", dev+" "+op+"="+strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// SetCPUShare sets the weight of each group in the same hierarchy.
func (cg *CGroup) SetCPUShare(limit int64) error {
	version := cgControllers["cpu"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "cpu", "cpu.shares", strconv.FormatInt(limit, 10))
	case V2:
		return cg.rw.Set(version, "cpu", "cpu.weight", strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// SetCPUCfsLimit sets the quota and duration in ms for each scheduling period.
func (cg *CGroup) SetCPUCfsLimit(limitPeriod int64, limitQuota int64) error {
	version := cgControllers["cpu"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		err := cg.rw.Set(version, "cpu", "cpu.cfs_quota_us", strconv.FormatInt(limitQuota, 10))
		if err != nil {
			return err
		}

		err = cg.rw.Set(version, "cpu", "cpu.cfs_period_us", strconv.FormatInt(limitPeriod, 10))
		if err != nil {
			return err
		}

		return nil
	case V2:
		if limitPeriod == -1 && limitQuota == -1 {
			return cg.rw.Set(version, "cpu", "cpu.max", "max")
		}

		return cg.rw.Set(version, "cpu", "cpu.max", strconv.FormatInt(limitQuota, 10)+" "+strconv.FormatInt(limitPeriod, 10))
	}

	return ErrUnknownVersion
}

// SetHugepagesLimit applies a limit to the number of processes.
func (cg *CGroup) SetHugepagesLimit(pageType string, limit int64) error {
	version := cgControllers["hugetlb"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "hugetlb", "hugetlb."+pageType+".limit_in_bytes", strconv.FormatInt(limit, 10))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "hugetlb", "hugetlb."+pageType+".max", "max")
		}

		return cg.rw.Set(version, "hugetlb", "hugetlb."+pageType+".max", strconv.FormatInt(limit, 10))
	}

	return ErrUnknownVersion
}

// GetEffectiveCpuset returns the current set of CPUs for the cgroup.
func (cg *CGroup) GetEffectiveCpuset() (string, error) {
	version := cgControllers["cpuset"]
	switch version {
	case Unavailable:
		return "", ErrControllerMissing
	case V1:
		return cg.rw.Get(version, "cpuset", "cpuset.effective_cpus")
	case V2:
		return cg.rw.Get(version, "cpuset", "cpuset.cpus.effective")
	}

	return "", ErrUnknownVersion
}

// GetCpuset returns the current set of CPUs for the cgroup.
func (cg *CGroup) GetCpuset() (string, error) {
	version := cgControllers["cpuset"]
	switch version {
	case Unavailable:
		return "", ErrControllerMissing
	case V1, V2:
		return cg.rw.Get(version, "cpuset", "cpuset.cpus")
	}

	return "", ErrUnknownVersion
}

// SetCpuset set the currently allowed set of CPUs for the cgroups.
func (cg *CGroup) SetCpuset(limit string) error {
	version := cgControllers["cpuset"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1, V2:
		return cg.rw.Set(version, "cpuset", "cpuset.cpus", limit)
	}

	return ErrUnknownVersion
}

// GetMemoryStats returns memory stats.
func (cg *CGroup) GetMemoryStats() (map[string]uint64, error) {
	var (
		err   error
		stats string
	)

	out := make(map[string]uint64)

	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return nil, ErrControllerMissing
	case V1, V2:
		stats, err = cg.rw.Get(version, "memory", "memory.stat")
	}

	if err != nil {
		return nil, err
	}

	for stat := range strings.SplitSeq(stats, "\n") {
		key, value, found := strings.Cut(stat, " ")
		if !found {
			continue
		}

		switch key {
		case "total_active_anon", "active_anon":
			out["active_anon"], _ = strconv.ParseUint(value, 10, 64)
		case "total_active_file", "active_file":
			out["active_file"], _ = strconv.ParseUint(value, 10, 64)
		case "total_inactive_anon", "inactive_anon":
			out["inactive_anon"], _ = strconv.ParseUint(value, 10, 64)
		case "total_inactive_file", "inactive_file":
			out["inactive_file"], _ = strconv.ParseUint(value, 10, 64)
		case "total_unevictable", "unevictable":
			out["unevictable"], _ = strconv.ParseUint(value, 10, 64)
		case "total_writeback", "file_writeback":
			out["writeback"], _ = strconv.ParseUint(value, 10, 64)
		case "total_dirty", "file_dirty":
			out["dirty"], _ = strconv.ParseUint(value, 10, 64)
		case "total_mapped_file", "file_mapped":
			out["mapped"], _ = strconv.ParseUint(value, 10, 64)
		case "total_rss": // v1 only
			out["rss"], _ = strconv.ParseUint(value, 10, 64)
		case "total_shmem", "shmem":
			out["shmem"], _ = strconv.ParseUint(value, 10, 64)
		case "total_cache", "file":
			out["cache"], _ = strconv.ParseUint(value, 10, 64)
		}
	}

	// Calculated values
	out["active"] = out["active_anon"] + out["active_file"]
	out["inactive"] = out["inactive_anon"] + out["inactive_file"]

	return out, nil
}

// GetOOMKills returns the number of oom kills.
func (cg *CGroup) GetOOMKills() (uint64, error) {
	var (
		err   error
		stats string
	)

	version := cgControllers["memory"]

	switch version {
	case V1:
		stats, err = cg.rw.Get(version, "memory", "memory.oom_control")
	case V2:
		stats, err = cg.rw.Get(version, "memory", "memory.events")
	default:
		return 0, ErrControllerMissing
	}

	if err != nil {
		return 0, err
	}

	for stat := range strings.SplitSeq(stats, "\n") {
		// Skip unrelated lines.
		value, found := strings.CutPrefix(stat, "oom_kill ")
		if !found {
			continue
		}

		out, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("Failed parsing %q: %w", value, err)
		}

		return out, nil
	}

	return 0, errors.New("Failed getting oom_kill")
}

// GetIOStats returns disk stats.
func (cg *CGroup) GetIOStats() (map[string]*IOStats, error) {
	partitions, err := os.ReadFile("/proc/partitions")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/partitions: %w", err)
	}

	// partMap maps major:minor to device names, e.g. 259:0 -> nvme0n1
	partMap := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(partitions))

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		// Ignore the header
		if fields[0] == "major" {
			continue
		}

		if len(fields) < 4 {
			continue
		}

		partMap[fields[0]+":"+fields[1]] = fields[3]
	}

	// ioMap contains io stats for each device
	ioMap := make(map[string]*IOStats)

	version := cgControllers["blkio"]
	switch version {
	case Unavailable:
		return nil, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "blkio", "blkio.throttle.io_service_bytes_recursive")
		if err != nil {
			return nil, fmt.Errorf("Failed getting blkio.throttle.io_service_bytes_recursive: %w", err)
		}

		scanner := bufio.NewScanner(strings.NewReader(val))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if len(fields) != 3 {
				continue
			}

			// Skip loop devices (major dev ID 7) as they are irrelevant.
			if strings.HasPrefix(fields[0], "7:") {
				continue
			}

			if ioMap[partMap[fields[0]]] == nil {
				ioMap[partMap[fields[0]]] = &IOStats{}
			}

			switch fields[1] {
			case "Read":
				ioMap[partMap[fields[0]]].ReadBytes, err = strconv.ParseUint(fields[2], 10, 64)
			case "Write":
				ioMap[partMap[fields[0]]].WrittenBytes, err = strconv.ParseUint(fields[2], 10, 64)
			}

			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q (%q) of blkio.throttle.io_service_bytes_recursive: %w", fields[1], fields[2], err)
			}
		}

		val, err = cg.rw.Get(version, "blkio", "blkio.throttle.io_serviced_recursive")
		if err != nil {
			return nil, fmt.Errorf("Failed getting blkio.throttle.io_serviced_recursive: %w", err)
		}

		scanner = bufio.NewScanner(strings.NewReader(val))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if len(fields) != 3 {
				continue
			}

			// Skip loop devices (major dev ID 7) as they are irrelevant.
			if strings.HasPrefix(fields[0], "7:") {
				continue
			}

			if ioMap[partMap[fields[0]]] == nil {
				ioMap[partMap[fields[0]]] = &IOStats{}
			}

			switch fields[1] {
			case "Read":
				ioMap[partMap[fields[0]]].ReadsCompleted, err = strconv.ParseUint(fields[2], 10, 64)
			case "Write":
				ioMap[partMap[fields[0]]].WritesCompleted, err = strconv.ParseUint(fields[2], 10, 64)
			}

			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q (%q) of blkio.throttle.io_serviced_recursive: %w", fields[1], fields[2], err)
			}
		}

		return ioMap, nil
	case V2:
		val, err := cg.rw.Get(version, "io", "io.stat")
		if err != nil {
			return nil, fmt.Errorf("Failed getting io.stat: %w", err)
		}

		scanner := bufio.NewScanner(strings.NewReader(val))

		for scanner.Scan() {
			var devID string
			ioStats := &IOStats{}

			// An io.stat line looks like this: "major:minor rbytes=[0-9]+ wbytes=[0-9]+ rios=[0-9]+ wios=[0-9]+ dbytes=[0-9]+ dios=[0-9]+".
			for statPart := range strings.SplitSeq(scanner.Text(), " ") {
				// If the stat part is empty, skip it.
				if statPart == "" {
					continue
				}

				if strings.Contains(statPart, ":") {
					// Store the last dev ID as this works around a kernel bug where multiple dev IDs could appear on a single line.
					devID = statPart
					continue
				}

				// Skip loop devices (major dev ID 7) as they are irrelevant.
				if strings.HasPrefix(devID, "7:") {
					continue
				}

				// Skip irrelevant stats related to direct IO (dbytes= and dios=).
				if strings.HasPrefix(statPart, "d") {
					continue
				}

				// Parse the stat value.
				statName, statValueStr, found := strings.Cut(statPart, "=")
				if !found {
					return nil, fmt.Errorf("Failed extracting io.stat %q (from %q)", statPart, scanner.Text())
				}

				statValue, err := strconv.ParseUint(statValueStr, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("Failed parsing io.stat %q %q (from %q): %w", statName, statValueStr, scanner.Text(), err)
				}

				switch statName {
				case "rbytes":
					ioStats.ReadBytes = statValue
				case "wbytes":
					ioStats.WrittenBytes = statValue
				case "rios":
					ioStats.ReadsCompleted = statValue
				case "wios":
					ioStats.WritesCompleted = statValue
				}
			}

			ioMap[partMap[devID]] = ioStats
		}

		return ioMap, nil
	}

	return nil, ErrUnknownVersion
}
