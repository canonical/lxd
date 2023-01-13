package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/locking"
	"github.com/lxc/lxd/lxd/metrics"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
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

	// Check if API is wide open.
	if !s.GlobalConfig.MetricsAuthentication() {
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
	s := d.State()

	projectName := queryParam(r, "project")

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
		return response.SyncResponsePlain(true, metricSet.String())
	}

	cacheDuration := time.Duration(8) * time.Second

	// Acquire update lock.
	lockCtx, lockCtxCancel := context.WithTimeout(r.Context(), cacheDuration)
	defer lockCtxCancel()

	unlock := locking.Lock(lockCtx, "metricsGet")
	if unlock == nil {
		return response.SmartError(api.StatusErrorf(http.StatusLocked, "Metrics are currently being built by another request"))
	}

	defer unlock()

	// Check if any of the missing data has been filled in since acquiring the lock.
	// As its possible another request was already populating the cache when we tried to take the lock.
	projectsToFetch = invalidProjectFilters(projectNames)

	// If all valid, return immediately.
	if len(projectsToFetch) == 0 {
		return response.SyncResponsePlain(true, metricSet.String())
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
					logger.Warn("Failed getting instance metrics", logger.Ctx{"instance": inst.Name(), "project": projectName, "err": err})
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
		// Ignore stopped instances.
		if !inst.IsRunning() {
			continue
		}

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

	for project, entries := range newMetrics {
		metricsCache[project] = metricsCacheEntry{
			expiry:  time.Now().Add(cacheDuration),
			metrics: entries,
		}

		metricSet.Merge(entries)
	}

	metricsCacheLock.Unlock()

	return response.SyncResponsePlain(true, metricSet.String())
}
