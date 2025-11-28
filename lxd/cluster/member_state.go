package cluster

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// getLoadAvgs returns the host's load averages from /proc/loadavg.
func getLoadAvgs() ([]float64, error) {
	loadAvgs := make([]float64, 3)

	loadAvgsBuf, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}

	loadAvgFields := strings.Fields(string(loadAvgsBuf))

	loadAvgs[0], err = strconv.ParseFloat(loadAvgFields[0], 64)
	if err != nil {
		return nil, err
	}

	loadAvgs[1], err = strconv.ParseFloat(loadAvgFields[1], 64)
	if err != nil {
		return nil, err
	}

	loadAvgs[2], err = strconv.ParseFloat(loadAvgFields[2], 64)
	if err != nil {
		return nil, err
	}

	return loadAvgs, nil
}

// LocalSysInfo retrieves system information about a cluster member.
func LocalSysInfo() (*api.ClusterMemberSysInfo, error) {
	// Get system info.
	info := unix.Sysinfo_t{}
	err := unix.Sysinfo(&info)
	if err != nil {
		logger.Warn("Failed getting sysinfo", logger.Ctx{"err": err})

		return nil, err
	}

	sysInfo := &api.ClusterMemberSysInfo{}

	// Account for different representations of Sysinfo_t on different architectures.
	sysInfo.Uptime = int64(info.Uptime)
	sysInfo.TotalRAM = uint64(info.Totalram)
	sysInfo.SharedRAM = uint64(info.Sharedram)
	sysInfo.BufferRAM = uint64(info.Bufferram)
	sysInfo.FreeRAM = uint64(info.Freeram)
	sysInfo.TotalSwap = uint64(info.Totalswap)
	sysInfo.FreeSwap = uint64(info.Freeswap)

	sysInfo.Processes = info.Procs
	sysInfo.LoadAverages, err = getLoadAvgs()
	if err != nil {
		return nil, fmt.Errorf("Failed getting load averages: %w", err)
	}

	// NumCPU gives the number of threads available to the LXD server at startup,
	// not the currently available number of threads.
	sysInfo.LogicalCPUs = uint64(runtime.NumCPU())

	return sysInfo, nil
}

// ClusterState returns a map from clusterMemberName -> state for every member
// of the cluster. This requires an HTTP call to the rest of the cluster.
func ClusterState(s *state.State, networkCert *shared.CertInfo, members ...db.NodeInfo) (map[string]api.ClusterMemberState, error) {
	serverCert := s.ServerCert()

	notifier, err := NewNotifier(s, networkCert, serverCert, NotifyAll, members...)
	if err != nil {
		return nil, err
	}

	type stateTuple struct {
		name  string
		state *api.ClusterMemberState
	}

	memberStates := make(map[string]api.ClusterMemberState)
	statesChan := make(chan stateTuple)

	var wg sync.WaitGroup
	wg.Go(func() {
		for state := range statesChan {
			memberStates[state.name] = *state.state
		}
	})

	err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
		state, _, err := client.GetClusterMemberState(member.Name)
		if err != nil {
			return err
		}

		statesChan <- stateTuple{
			name:  member.Name,
			state: state,
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	close(statesChan)

	includeLocalMember := len(members) == 0
	for _, member := range members {
		if member.Name == s.ServerName {
			includeLocalMember = true
			break
		}
	}

	wg.Wait()

	if includeLocalMember {
		localState, err := MemberState(context.TODO(), s)
		if err != nil {
			return nil, fmt.Errorf("Failed to get local member state: %w", err)
		}

		memberStates[s.ServerName] = *localState
	}

	return memberStates, nil
}

// MemberState retrieves state information about the cluster member.
func MemberState(ctx context.Context, s *state.State) (*api.ClusterMemberState, error) {
	var memberState api.ClusterMemberState

	sysInfo, err := LocalSysInfo()
	if err != nil {
		return nil, err
	}

	memberState.SysInfo = *sysInfo

	// Get storage pool states.
	stateCreated := db.StoragePoolCreated

	var pools map[int64]api.StoragePool
	var poolMembers map[int64]map[int64]db.StoragePoolNode

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		pools, poolMembers, err = tx.GetStoragePools(ctx, &stateCreated)

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Failed loading storage pools: %w", err)
	}

	memberState.StoragePools = make(map[string]api.StoragePoolState, len(pools))

	for poolID := range pools {
		pool, err := storagePools.LoadByRecord(s, poolID, pools[poolID], poolMembers[poolID])
		if err != nil {
			return nil, fmt.Errorf("Failed loading storage pool %q: %w", pools[poolID].Name, err)
		}

		res, err := pool.GetResources()
		if err != nil {
			return nil, fmt.Errorf("Failed getting storage pool resources %q: %w", pools[poolID].Name, err)
		}

		memberState.StoragePools[pools[poolID].Name] = api.StoragePoolState{
			ResourcesStoragePool: *res,
		}
	}

	return &memberState, nil
}
