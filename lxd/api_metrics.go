package main

import (
	"net/http"
	"sync"
	"time"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/db"
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

	Get: APIEndpointAction{Handler: metricsGet, AccessHandler: allowProjectPermission("containers", "view")},
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

	// Figure out the projects to retrieve.
	var projectNames []string

	if projectName != "" {
		projectNames = []string{projectName}
	} else {
		// Get all projects.
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			projects, err := tx.GetProjects(db.ProjectFilter{})
			if err != nil {
				return err
			}

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
	resp := metrics.NewMetricSet(nil)

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
		resp.Merge(cache.metrics)
	}
	metricsCacheLock.Unlock()

	// If all valid, return immediately.
	if len(projectMissing) == 0 {
		return response.SyncResponsePlain(true, resp.String())
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
		resp.Merge(cache.metrics)
	}
	metricsCacheLock.Unlock()

	// If all valid, return immediately.
	if len(toFetch) == 0 {
		return response.SyncResponsePlain(true, resp.String())
	}

	// Prepare temporary metrics storage.
	newMetrics := map[string]*metrics.MetricSet{}
	newMetricsLock := sync.Mutex{}

	// Fetch what's missing.
	wgInstances := sync.WaitGroup{}
	for _, project := range toFetch {
		newMetrics[project] = metrics.NewMetricSet(nil)

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

				instanceMetrics, err := inst.Metrics()
				if err != nil {
					logger.Warn("Failed to get instance metrics", log.Ctx{"instance": inst.Name(), "project": inst.Project(), "err": err})
					return
				}

				// Add the metrics.
				newMetricsLock.Lock()
				defer newMetricsLock.Unlock()

				newMetrics[inst.Project()].Merge(instanceMetrics)
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
			expiry:  time.Now().Add(15 * time.Second),
			metrics: entries,
		}

		resp.Merge(entries)
	}
	metricsCacheLock.Unlock()

	return response.SyncResponsePlain(true, resp.String())
}
