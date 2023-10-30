package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance"
	instanceDrivers "github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

type metricsCacheEntry struct {
	metrics *metrics.MetricSet
	expiry  time.Time
}

var metricsCache map[string]metricsCacheEntry
var metricsCacheLock sync.Mutex

var metricsCmd = APIEndpoint{
	Path: "metrics",

	Get: APIEndpointAction{Handler: metricsGet, AccessHandler: allowMetrics, AllowUntrusted: true},
}

func allowMetrics(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if !s.GlobalConfig.MetricsAuthentication() {
		return response.EmptySyncResponse
	}

	return allowPermission(auth.ObjectTypeServer, auth.EntitlementCanViewMetrics)(d, r)
}

// swagger:operation GET /1.0/metrics metrics metrics_get
//
//	Get metrics
//
//	Gets metrics of instances.
//
//	---
//	produces:
//	  - text/plain
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "200":
//	    description: Metrics
//	    schema:
//	      type: string
//	      description: Instance metrics
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func metricsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.QueryParam(r, "project")
	compress := strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")

	// Forward if requested.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Wait until daemon is fully started.
	<-d.waitReady.Done()

	// Prepare response.
	metricSet := metrics.NewMetricSet(nil)

	var projectNames []string

	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Figure out the projects to retrieve.
		if projectName != "" {
			projectNames = []string{projectName}
		} else {
			// Get all project names if no specific project requested.
			projects, err := dbCluster.GetProjects(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}

			projectNames = make([]string, 0, len(projects))
			for _, project := range projects {
				projectNames = append(projectNames, project.Name)
			}
		}

		// Add internal metrics.
		metricSet.Merge(internalMetrics(ctx, s.StartTime, tx))

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// LXD server information
	metricSet.Merge(serverMetrics(s, projectName))

	// invalidProjectFilters returns project filters which are either not in cache or have expired.
	invalidProjectFilters := func(projectNames []string) []dbCluster.InstanceFilter {
		metricsCacheLock.Lock()
		defer metricsCacheLock.Unlock()

		var filters []dbCluster.InstanceFilter
		for _, p := range projectNames {
			projectName := p // Local var for filter pointer.

			cache, ok := metricsCache[projectName]
			if !ok || cache.expiry.Before(time.Now()) {
				// If missing or expired, record it.
				filters = append(filters, dbCluster.InstanceFilter{
					Project: &projectName,
					Node:    &s.ServerName,
				})

				continue
			}

			// If present and valid, merge the existing data.
			metricSet.Merge(cache.metrics)
		}

		return filters
	}

	// Review the cache for invalid projects.
	projectsToFetch := invalidProjectFilters(projectNames)

	// If all valid, return immediately.
	if len(projectsToFetch) == 0 {
		return getFilteredMetrics(s, r, compress, metricSet)
	}

	cacheDuration := time.Duration(8) * time.Second

	// Acquire update lock.
	lockCtx, lockCtxCancel := context.WithTimeout(r.Context(), cacheDuration)
	defer lockCtxCancel()

	unlock, err := locking.Lock(lockCtx, "metricsGet")
	if err != nil {
		return response.SmartError(api.StatusErrorf(http.StatusLocked, "Metrics are currently being built by another request: %s", err))
	}

	defer unlock()

	// Setup a new response.
	metricSet = metrics.NewMetricSet(nil)

	// Check if any of the missing data has been filled in since acquiring the lock.
	// As its possible another request was already populating the cache when we tried to take the lock.
	projectsToFetch = invalidProjectFilters(projectNames)

	// If all valid, return immediately.
	if len(projectsToFetch) == 0 {
		return getFilteredMetrics(s, r, compress, metricSet)
	}

	// Gather information about host interfaces once.
	hostInterfaces, _ := net.Interfaces()

	var instances []instance.Instance
	err = s.DB.Cluster.InstanceList(r.Context(), func(dbInst db.InstanceArgs, p api.Project) error {
		inst, err := instance.Load(s, dbInst, p)
		if err != nil {
			return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
		}

		instances = append(instances, inst)

		return nil
	}, projectsToFetch...)
	if err != nil {
		return response.SmartError(err)
	}

	// Prepare temporary metrics storage.
	newMetrics := make(map[string]*metrics.MetricSet, len(projectsToFetch))
	newMetricsLock := sync.Mutex{}

	// Limit metrics build concurrency to number of instances or number of CPU cores (which ever is less).
	var wg sync.WaitGroup
	instMetricsCh := make(chan instance.Instance)
	maxConcurrent := runtime.NumCPU()
	instCount := len(instances)
	if instCount < maxConcurrent {
		maxConcurrent = instCount
	}

	// Start metrics builder routines.
	for i := 0; i < maxConcurrent; i++ {
		go func(instMetricsCh <-chan instance.Instance) {
			for inst := range instMetricsCh {
				projectName := inst.Project().Name
				instanceMetrics, err := inst.Metrics(hostInterfaces)
				if err != nil {
					// Ignore stopped instances.
					if !errors.Is(err, instanceDrivers.ErrInstanceIsStopped) {
						logger.Warn("Failed getting instance metrics", logger.Ctx{"instance": inst.Name(), "project": projectName, "err": err})
					}
				} else {
					// Add the metrics.
					newMetricsLock.Lock()

					// Initialise metrics set for project if needed.
					if newMetrics[projectName] == nil {
						newMetrics[projectName] = metrics.NewMetricSet(nil)
					}

					newMetrics[projectName].Merge(instanceMetrics)

					newMetricsLock.Unlock()
				}

				wg.Done()
			}
		}(instMetricsCh)
	}

	// Fetch what's missing.
	for _, inst := range instances {
		wg.Add(1)
		instMetricsCh <- inst
	}

	wg.Wait()
	close(instMetricsCh)

	// Put the new data in the global cache and in response.
	metricsCacheLock.Lock()

	if metricsCache == nil {
		metricsCache = map[string]metricsCacheEntry{}
	}

	updatedProjects := []string{}
	for project, entries := range newMetrics {
		metricsCache[project] = metricsCacheEntry{
			expiry:  time.Now().Add(cacheDuration),
			metrics: entries,
		}

		updatedProjects = append(updatedProjects, project)
		metricSet.Merge(entries)
	}

	for _, project := range projectsToFetch {
		if shared.ValueInSlice(*project.Project, updatedProjects) {
			continue
		}

		metricsCache[*project.Project] = metricsCacheEntry{
			expiry: time.Now().Add(cacheDuration),
		}
	}

	metricsCacheLock.Unlock()

	return getFilteredMetrics(s, r, compress, metricSet)
}

func getFilteredMetrics(s *state.State, r *http.Request, compress bool, metricSet *metrics.MetricSet) response.Response {
	if !s.GlobalConfig.MetricsAuthentication() {
		return response.SyncResponsePlain(true, compress, metricSet.String())
	}

	// Get instances the user is allowed to view.
	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeInstance)
	if err != nil && !api.StatusErrorCheck(err, http.StatusForbidden) {
		return response.SmartError(err)
	} else if err != nil {
		// This is counterintuitive. We are unauthorized to get a permission checker for viewing instances because a metric type certificate
		// can't view instances. However, in order to get to this point we must already have auth.EntitlementCanViewMetrics. So we can view
		// the metrics but we can't do any filtering, so just return the metrics.
		return response.SyncResponsePlain(true, compress, metricSet.String())
	}

	metricSet.FilterSamples(userHasPermission)

	return response.SyncResponsePlain(true, compress, metricSet.String())
}

func internalMetrics(ctx context.Context, daemonStartTime time.Time, tx *db.ClusterTx) *metrics.MetricSet {
	out := metrics.NewMetricSet(nil)

	warnings, err := dbCluster.GetWarnings(ctx, tx.Tx())
	if err != nil {
		logger.Warn("Failed to get warnings", logger.Ctx{"err": err})
	} else {
		// Total number of warnings
		out.AddSamples(metrics.WarningsTotal, metrics.Sample{Value: float64(len(warnings))})
	}

	operations, err := dbCluster.GetOperations(ctx, tx.Tx())
	if err != nil {
		logger.Warn("Failed to get operations", logger.Ctx{"err": err})
	} else {
		// Total number of operations
		out.AddSamples(metrics.OperationsTotal, metrics.Sample{Value: float64(len(operations))})
	}

	// Daemon uptime
	out.AddSamples(metrics.UptimeSeconds, metrics.Sample{Value: time.Since(daemonStartTime).Seconds()})

	// Number of goroutines
	out.AddSamples(metrics.GoGoroutines, metrics.Sample{Value: float64(runtime.NumGoroutine())})

	// Go memory stats
	var ms runtime.MemStats

	runtime.ReadMemStats(&ms)

	out.AddSamples(metrics.GoAllocBytes, metrics.Sample{Value: float64(ms.Alloc)})
	out.AddSamples(metrics.GoAllocBytesTotal, metrics.Sample{Value: float64(ms.TotalAlloc)})
	out.AddSamples(metrics.GoBuckHashSysBytes, metrics.Sample{Value: float64(ms.BuckHashSys)})
	out.AddSamples(metrics.GoFreesTotal, metrics.Sample{Value: float64(ms.Frees)})
	out.AddSamples(metrics.GoGCSysBytes, metrics.Sample{Value: float64(ms.GCSys)})
	out.AddSamples(metrics.GoHeapAllocBytes, metrics.Sample{Value: float64(ms.HeapAlloc)})
	out.AddSamples(metrics.GoHeapIdleBytes, metrics.Sample{Value: float64(ms.HeapIdle)})
	out.AddSamples(metrics.GoHeapInuseBytes, metrics.Sample{Value: float64(ms.HeapInuse)})
	out.AddSamples(metrics.GoHeapObjects, metrics.Sample{Value: float64(ms.HeapObjects)})
	out.AddSamples(metrics.GoHeapReleasedBytes, metrics.Sample{Value: float64(ms.HeapReleased)})
	out.AddSamples(metrics.GoHeapSysBytes, metrics.Sample{Value: float64(ms.HeapSys)})
	out.AddSamples(metrics.GoLookupsTotal, metrics.Sample{Value: float64(ms.Lookups)})
	out.AddSamples(metrics.GoMallocsTotal, metrics.Sample{Value: float64(ms.Mallocs)})
	out.AddSamples(metrics.GoMCacheInuseBytes, metrics.Sample{Value: float64(ms.MCacheInuse)})
	out.AddSamples(metrics.GoMCacheSysBytes, metrics.Sample{Value: float64(ms.MCacheSys)})
	out.AddSamples(metrics.GoMSpanInuseBytes, metrics.Sample{Value: float64(ms.MSpanInuse)})
	out.AddSamples(metrics.GoMSpanSysBytes, metrics.Sample{Value: float64(ms.MSpanSys)})
	out.AddSamples(metrics.GoNextGCBytes, metrics.Sample{Value: float64(ms.NextGC)})
	out.AddSamples(metrics.GoOtherSysBytes, metrics.Sample{Value: float64(ms.OtherSys)})
	out.AddSamples(metrics.GoStackInuseBytes, metrics.Sample{Value: float64(ms.StackInuse)})
	out.AddSamples(metrics.GoStackSysBytes, metrics.Sample{Value: float64(ms.StackSys)})
	out.AddSamples(metrics.GoSysBytes, metrics.Sample{Value: float64(ms.Sys)})

	return out
}

func serverMetrics(s *state.State, projectName string) *metrics.MetricSet {
	serverInfo := map[string]string{
		"version":             version.Version,
		"os_name":             s.OS.ReleaseInfo["NAME"],
		"os_version":          s.OS.ReleaseInfo["VERSION_ID"],
		"firewall":            s.Firewall.String(),
		"kernel_architecture": s.OS.Uname.Machine,
		"kernel_version":      s.OS.Uname.Release,
	}

	clustered, err := cluster.Enabled(s.DB.Node)
	if err == nil {
		serverInfo["clustered"] = strconv.FormatBool(clustered)
	}

	certificate := string(s.Endpoints.NetworkPublicKey())
	var certificateFingerprint string
	if certificate != "" {
		certificateFingerprint, err = shared.CertFingerprintStr(certificate)
		if err == nil {
			serverInfo["certificate_fingerprint"] = certificateFingerprint
		}
	}

	out := metrics.NewMetricSet(nil)

	out.AddSamples(metrics.ServerInfo, metrics.Sample{Labels: serverInfo, Value: 1})

	// Kernel features
	kernelFeatures := map[string]string{
		"netnsid_getifaddrs":        fmt.Sprintf("%v", s.OS.NetnsGetifaddrs),
		"uevent_injection":          fmt.Sprintf("%v", s.OS.UeventInjection),
		"unpriv_fscaps":             fmt.Sprintf("%v", s.OS.VFS3Fscaps),
		"seccomp_listener":          fmt.Sprintf("%v", s.OS.SeccompListener),
		"seccomp_listener_continue": fmt.Sprintf("%v", s.OS.SeccompListenerContinue),
		"shiftfs":                   fmt.Sprintf("%v", s.OS.Shiftfs),
		"idmapped_mounts":           fmt.Sprintf("%v", s.OS.IdmappedMounts),
	}

	out.AddSamples(metrics.KernelFeaturesInfo, metrics.Sample{Labels: kernelFeatures, Value: 1})

	// LXC features
	if s.OS.LXCFeatures != nil {
		lxcFeatures := make(map[string]string)

		for k, v := range s.OS.LXCFeatures {
			lxcFeatures[k] = fmt.Sprintf("%v", v)
		}

		out.AddSamples(metrics.LXCFeaturesInfo, metrics.Sample{Labels: lxcFeatures, Value: 1})
	}

	// Storage driver info
	storageDriverInfo := make(map[string]string)

	supportedStorageDrivers, _ := readStoragePoolDriversCache()
	for _, driver := range supportedStorageDrivers {
		storageDriverInfo[driver.Name] = driver.Version
	}

	out.AddSamples(metrics.StorageDriverInfo, metrics.Sample{Labels: storageDriverInfo, Value: 1})

	// Instance driver info
	instanceDriverInfo := make(map[string]string)

	drivers := instanceDrivers.DriverStatuses()
	for _, driver := range drivers {
		// Only report the supported drivers.
		if !driver.Supported {
			continue
		}

		instanceDriverInfo[driver.Info.Name] = driver.Info.Version
	}

	out.AddSamples(metrics.InstanceDriverInfo, metrics.Sample{Labels: instanceDriverInfo, Value: 1})

	return out
}
