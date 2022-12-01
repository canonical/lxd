package main

import (
	"context"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/metrics"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared/logger"
)

type metricsCacheEntry struct {
	metrics *metrics.MetricSet
	expiry  time.Time
}

var metricsCache map[string]metricsCacheEntry
var metricsCacheLock sync.Mutex
var metricsLock sync.Mutex

var metricsCmd = APIEndpoint{
	Path: "metrics",

	Get: APIEndpointAction{Handler: metricsGet, AccessHandler: allowMetrics, AllowUntrusted: true},
}

func allowMetrics(d *Daemon, r *http.Request) response.Response {
	// Check if API is wide open.
	if !d.State().GlobalConfig.MetricsAuthentication() {
		return response.EmptySyncResponse
	}

	// If not wide open, apply project access restrictions.
	return allowProjectPermission("containers", "view")(d, r)
}

// swagger:operation GET /1.0/metrics metrics metrics_get
//
// Get metrics
//
// Gets metrics of instances.
//
// ---
// produces:
//   - text/plain
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: target
//     description: Cluster member name
//     type: string
//     example: lxd01
// responses:
//   "200":
//     description: Metrics
//     schema:
//       type: string
//       description: Instance metrics
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func metricsGet(d *Daemon, r *http.Request) response.Response {
	projectName := queryParam(r, "project")

	// Forward if requested.
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// Wait until daemon is fully started.
	<-d.waitReady.Done()

	// Figure out the projects to retrieve.
	var projectNames []string

	if projectName != "" {
		projectNames = []string{projectName}
	} else {
		// Get all projects.
		err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			projects, err := dbCluster.GetProjects(ctx, tx.Tx())
			if err != nil {
				return err
			}

			projectNames = make([]string, 0, len(projects))
			for _, project := range projects {
				projectNames = append(projectNames, project.Name)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Prepare response.
	metricSet := metrics.NewMetricSet(nil)

	// Add internal metrics.
	metricSet.Merge(internalMetrics(d))

	// Review the cache.
	metricsCacheLock.Lock()
	projectMissing := []string{}
	for _, project := range projectNames {
		cache, ok := metricsCache[project]
		if !ok || cache.expiry.Before(time.Now()) {
			// If missing or expired, record it.
			projectMissing = append(projectMissing, project)
			continue
		}

		// If present and valid, merge the existing data.
		metricSet.Merge(cache.metrics)
	}

	metricsCacheLock.Unlock()

	// If all valid, return immediately.
	if len(projectMissing) == 0 {
		return response.SyncResponsePlain(true, metricSet.String())
	}

	// Acquire update lock.
	metricsLock.Lock()
	defer metricsLock.Unlock()

	// Check if any of the missing data has been filled in.
	metricsCacheLock.Lock()
	toFetch := []string{}
	for _, project := range projectMissing {
		cache, ok := metricsCache[project]
		if !ok || cache.expiry.Before(time.Now()) {
			// Still missing, queue a re-fetch.
			toFetch = append(toFetch, project)
			continue
		}

		// If present and valid, merge the existing data.
		metricSet.Merge(cache.metrics)
	}

	metricsCacheLock.Unlock()

	// If all valid, return immediately.
	if len(toFetch) == 0 {
		return response.SyncResponsePlain(true, metricSet.String())
	}

	hostInterfaces, _ := net.Interfaces()

	// Prepare temporary metrics storage.
	newMetrics := map[string]*metrics.MetricSet{}
	newMetricsLock := sync.Mutex{}

	// Fetch what's missing.
	wgInstances := sync.WaitGroup{}
	for _, project := range toFetch {
		// Get the instances.
		instances, err := instanceLoadNodeProjectAll(d.State(), project, instancetype.Any)
		if err != nil {
			return response.SmartError(err)
		}

		for _, inst := range instances {
			// Ignore stopped instances.
			if !inst.IsRunning() {
				continue
			}

			wgInstances.Add(1)
			go func(inst instance.Instance) {
				defer wgInstances.Done()

				projectName := inst.Project().Name
				instanceMetrics, err := inst.Metrics(hostInterfaces)
				if err != nil {
					logger.Warn("Failed to get instance metrics", logger.Ctx{"instance": inst.Name(), "project": projectName, "err": err})
					return
				}

				// Add the metrics.
				newMetricsLock.Lock()
				defer newMetricsLock.Unlock()

				// Initialise metrics set for project if needed.
				if newMetrics[projectName] == nil {
					newMetrics[projectName] = metrics.NewMetricSet(nil)
				}

				newMetrics[projectName].Merge(instanceMetrics)
			}(inst)
		}
	}

	wgInstances.Wait()

	// Put the new data in the global cache and in response.
	metricsCacheLock.Lock()

	if metricsCache == nil {
		metricsCache = map[string]metricsCacheEntry{}
	}

	for project, entries := range newMetrics {
		metricsCache[project] = metricsCacheEntry{
			expiry:  time.Now().Add(8 * time.Second),
			metrics: entries,
		}

		metricSet.Merge(entries)
	}

	metricsCacheLock.Unlock()

	return response.SyncResponsePlain(true, metricSet.String())
}

func internalMetrics(d *Daemon) *metrics.MetricSet {
	out := metrics.NewMetricSet(nil)

	_ = d.db.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
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

		return nil
	})

	// Daemon uptime
	out.AddSamples(metrics.UptimeSeconds, metrics.Sample{Value: time.Since(d.startTime).Seconds()})

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
