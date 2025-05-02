package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/resources"
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
	wg.Add(1)
	go func() {
		for state := range statesChan {
			memberStates[state.name] = *state.state
		}

		wg.Done()
	}()

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

// resourcesCacheMu protects api.Resources shared cache.
var resourcesCacheMu sync.Mutex

func getClusterMemberResourcesFromCache(nodeName string) (*api.Resources, error) {
	// Attempt to load the cached resources.
	resourcesPath := shared.CachePath("resources", fmt.Sprintf("%s.yaml", nodeName))

	data, err := os.ReadFile(resourcesPath)
	if err != nil {
		return nil, err
	}

	res := &api.Resources{}
	err = yaml.Unmarshal(data, res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func getClusterMemberResources(s *state.State, nodeInfo db.NodeInfo) (*api.Resources, error) {
	resourcesCacheMu.Lock()
	defer resourcesCacheMu.Unlock()

	// Check if we have a recent local cache entry already.
	resourcesPath := shared.CachePath("resources", fmt.Sprintf("%s.yaml", nodeInfo.Name))
	fi, err := os.Stat(resourcesPath)
	if err == nil && fi.ModTime().Before(time.Now().Add(time.Hour)) {
		return getClusterMemberResourcesFromCache(nodeInfo.Name)
	}

	// Connect to the server.
	client, err := Connect(nodeInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
	if err != nil {
		return nil, err
	}

	// Get the server resources.
	resources, err := client.GetServerResources()
	if err != nil {
		return nil, err
	}

	// Write to cache.
	data, err := json.Marshal(resources)
	if err != nil {
		return nil, err
	}

	err = os.WriteFile(resourcesPath, data, 0600)
	if err != nil {
		return nil, err
	}

	return resources, nil
}

// ClusterMembersResources returns a map from clusterMemberName -> Resources for every member
// of the cluster. This may require an HTTP call to the rest of the cluster.
func ClusterMembersResources(s *state.State) (map[string]api.Resources, []string, error) {
	membersResources := make(map[string]api.Resources)
	skippedMembers := []string{}

	if s.DB == nil || s.DB.Cluster == nil {
		return nil, skippedMembers, fmt.Errorf("Failed to get cluster members resources, global database is not initialised")
	}

	// Get the list of cluster members.
	var members []db.NodeInfo
	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, skippedMembers, err
	}

	for _, member := range members {
		var res *api.Resources

		if member.Name == s.ServerName {
			// Get our own resources info.
			res, err = resources.GetResources()
			if err != nil {
				return nil, skippedMembers, fmt.Errorf("Failed to get local member resources: %w", err)
			}
		} else {
			res, err = getClusterMemberResources(s, member)
			if err != nil {
				logger.Warn("Failed to get cluster member resources", logger.Ctx{"name": member.Name, "err": err})
				skippedMembers = append(skippedMembers, member.Name)
				continue
			}
		}

		membersResources[member.Name] = *res
	}

	return membersResources, skippedMembers, nil
}
