package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance"
	instanceDrivers "github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
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

	entityType := entity.TypeServer

	// Check for individual permissions on project if set.
	projectName := request.QueryParam(r, "project")
	if projectName != "" {
		entityType = entity.TypeProject
	}

	return allowPermission(entityType, auth.EntitlementCanViewMetrics)(d, r)
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
	var intMetrics *metrics.MetricSet
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

		// Register internal metrics.
		intMetrics = internalMetrics(ctx, s.StartTime, tx)
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

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
		metricSet.Merge(intMetrics)
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
		metricSet.Merge(intMetrics)
		return getFilteredMetrics(s, r, compress, metricSet)
	}

	// Gather information about host interfaces once.
	hostInterfaces, _ := net.Interfaces()

	var instances []instance.Instance
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			inst, err := instance.Load(s, dbInst, p)
			if err != nil {
				return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
			}

			instances = append(instances, inst)

			return nil
		}, projectsToFetch...)
	})
	if err != nil {
		return response.SmartError(err)
	}

	allProjectInstances := make(map[string]map[instancetype.Type]int)

	// Total number of instances, both running and stopped.
	for _, instance := range instances {
		projectName := instance.Project().Name

		_, ok := allProjectInstances[projectName]
		if !ok {
			allProjectInstances[projectName] = make(map[instancetype.Type]int)
		}

		allProjectInstances[projectName][instance.Type()]++
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
				if err != nil && !errors.Is(err, instanceDrivers.ErrInstanceIsStopped) {
					logger.Warn("Failed getting instance metrics", logger.Ctx{"instance": inst.Name(), "project": projectName, "err": err})
				} else {
					newMetricsLock.Lock()

					// Initialise metrics set for project if needed.
					// Do this even if the instance is stopped, otherwise the instance counter
					// metrics won't be included if this is the only instance in the project.
					// As won't be taken as a "new" metric when looping over newMetrics below.
					if newMetrics[projectName] == nil {
						newMetrics[projectName] = metrics.NewMetricSet(nil)
					}

					// Add the metrics if available.
					if instanceMetrics != nil {
						newMetrics[projectName].Merge(instanceMetrics)
					}

					newMetricsLock.Unlock()
				}

				wg.Done()
			}
		}(instMetricsCh)
	}

	// Fetch what's missing.
	wg.Add(len(instances))
	for _, inst := range instances {
		instMetricsCh <- inst
	}

	wg.Wait()
	close(instMetricsCh)

	// Put the new data in the global cache and in response.
	metricsCacheLock.Lock()

	if metricsCache == nil {
		metricsCache = map[string]metricsCacheEntry{}
	}

	// Add counter metrics for instances to the metric cache.
	// We need to do create a distinct metric set for each project.
	counterMetrics := make(map[string]*metrics.MetricSet, len(allProjectInstances))
	for project, instanceCountMap := range allProjectInstances {
		counterMetricSetPerProject := metrics.NewMetricSet(nil)
		counterMetricSetPerProject.AddSamples(
			metrics.Instances,
			metrics.Sample{
				Labels: map[string]string{"project": project, "type": instancetype.Container.String()},
				Value:  float64(instanceCountMap[instancetype.Container]),
			},
		)
		counterMetricSetPerProject.AddSamples(
			metrics.Instances,
			metrics.Sample{
				Labels: map[string]string{"project": project, "type": instancetype.VM.String()},
				Value:  float64(instanceCountMap[instancetype.VM]),
			},
		)

		counterMetrics[project] = counterMetricSetPerProject
	}

	updatedProjects := []string{}
	for project, entries := range newMetrics {
		counterMetric, ok := counterMetrics[project]
		if ok {
			entries.Merge(counterMetric)
		}

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

	metricSet.Merge(intMetrics) // Include the internal metrics after caching so they are not cached.

	return getFilteredMetrics(s, r, compress, metricSet)
}

func getFilteredMetrics(s *state.State, r *http.Request, compress bool, metricSet *metrics.MetricSet) response.Response {
	// Ignore filtering in case the authentication for metrics is disabled.
	if !s.GlobalConfig.MetricsAuthentication() {
		return response.SyncResponsePlain(true, compress, metricSet.String())
	}

	userHasProjectPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanViewMetrics, entity.TypeProject)
	if err != nil {
		return response.SmartError(err)
	}

	userHasServerPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanViewMetrics, entity.TypeServer)
	if err != nil {
		return response.SmartError(err)
	}

	// Filter all metrics for view permissions.
	// Internal server metrics without labels are compared against the server entity.
	metricSet.FilterSamples(func(labels map[string]string) bool {
		if labels["project"] != "" {
			return userHasProjectPermission(entity.ProjectURL(labels["project"]))
		}

		return userHasServerPermission(entity.ServerURL())
	})

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

	// API request metrics
	for _, entityType := range entity.APIMetricsEntityTypes() {
		out.AddSamples(
			metrics.APIOngoingRequests,
			metrics.Sample{
				Labels: map[string]string{"entity_type": entityType.String()},
				Value:  float64(metrics.GetOngoingRequests(entityType)),
			},
		)

		for result, resultName := range metrics.GetRequestResultsNames() {
			out.AddSamples(
				metrics.APICompletedRequests,
				metrics.Sample{
					Labels: map[string]string{"entity_type": entityType.String(), "result": resultName},
					Value:  float64(metrics.GetCompletedRequests(entityType, result)),
				},
			)
		}
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
