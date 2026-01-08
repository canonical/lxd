package cgroup

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/instance/instancetype"
)

// DeviceSchedRebalance channel for scheduling a CPU rebalance.
var DeviceSchedRebalance = make(chan []string, 2)

// TaskSchedulerTrigger triggers a CPU rebalance.
func TaskSchedulerTrigger(srcType instancetype.Type, srcName string, srcStatus string) {
	// Spawn a go routine which then triggers the scheduler
	select {
	case DeviceSchedRebalance <- []string{srcType.String(), srcName, srcStatus}:
	default:
		// Channel is full, drop the event
	}
}

// ParseCPU parses CPU allowances.
func ParseCPU(cpuAllowance string, cpuPriority string) (cpuShares int64, cpuCfsQuota int64, cpuCfsPeriod int64, err error) {
	// Max shares depending on backend.
	maxShares := int64(1024)
	if cgControllers["cpu"] == V2 {
		maxShares = 100
	}

	// Parse priority
	cpuShares = 0
	cpuPriorityInt := 10
	if cpuPriority != "" {
		cpuPriorityInt, err = strconv.Atoi(cpuPriority)
		if err != nil {
			return -1, -1, -1, err
		}
	}
	cpuShares -= int64(10 - cpuPriorityInt)

	// Parse allowance
	cpuCfsQuota = -1
	cpuCfsPeriod = 100000
	if cgControllers["cpu"] == V2 {
		cpuCfsPeriod = -1
	}

	if cpuAllowance != "" {
		percentStr, isPercentage := strings.CutSuffix(cpuAllowance, "%")
		if isPercentage {
			// Percentage based allocation
			percent, err := strconv.Atoi(percentStr)
			if err != nil {
				return -1, -1, -1, err
			}

			cpuShares += int64(float64(maxShares) / float64(100) * float64(percent))
		} else {
			// Time based allocation
			fields := strings.SplitN(cpuAllowance, "/", 2)
			if len(fields) != 2 {
				return -1, -1, -1, fmt.Errorf("Invalid allowance: %s", cpuAllowance)
			}

			quota, err := strconv.Atoi(strings.TrimSuffix(fields[0], "ms"))
			if err != nil {
				return -1, -1, -1, err
			}

			period, err := strconv.Atoi(strings.TrimSuffix(fields[1], "ms"))
			if err != nil {
				return -1, -1, -1, err
			}

			// Set limit in ms
			cpuCfsQuota = int64(quota * 1000)
			cpuCfsPeriod = int64(period * 1000)
			cpuShares += maxShares
		}
	} else {
		// Default is 100%
		cpuShares += maxShares
	}

	// Deal with a potential negative score
	if cpuShares < 0 {
		cpuShares = 0
	}

	return cpuShares, cpuCfsQuota, cpuCfsPeriod, nil
}
