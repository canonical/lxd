package main

import (
	"net/http"
	"time"

	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/metrics"
	"github.com/lxc/lxd/lxd/response"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

var metricsCmd = APIEndpoint{
	Path: "metrics",

	Get: APIEndpointAction{Handler: metricsGet, AccessHandler: allowProjectPermission("containers", "view")},
}

var metricsGetBuilding bool

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
	// Prevent concurrent comparison/modification of metricsLastFetchedTime and access of d.metrics.
	d.metricsMutex.Lock()

	// Return cached metrics build in progress or if they have been built in the last 15 seconds.
	if d.metrics != nil && (metricsGetBuilding || d.metricsLastBuildTime.Add(15*time.Second).After(time.Now())) {
		metricsStr := d.metrics.String()
		d.metricsMutex.Unlock()

		return response.SyncResponsePlain(true, metricsStr)
	}

	// Start metrics build and release lock so other requests can access cached metrics whilst we build.
	// Access to d.metricsLastBuildTime and d.metrics is forbidden from here on until we lock again later,
	// so the new metrics set will be built locally and stored in d.metrics at the end.
	metricsGetBuilding = true
	d.metricsMutex.Unlock()

	instances, err := instance.LoadNodeAll(d.State(), instancetype.Any)
	if err != nil {
		return response.SmartError(err)
	}

	// Launch concurrent gather of metrics from instances.
	instCount := 0
	projectName := queryParam(r, "project")
	metricsToMerge := make(chan *metrics.MetricSet)

	for _, inst := range instances {
		// If project has been specified, only consider only instances in this project
		if projectName != "" && projectName != inst.Project() {
			continue
		}

		// Ignore stopped instances
		if !inst.IsRunning() {
			continue
		}

		instCount++

		go func(inst instance.Instance) {
			instanceMetrics, err := inst.Metrics()
			if err != nil {
				metricsToMerge <- nil
				logger.Warn("Failed to get instance metrics", log.Ctx{"instance": inst.Name(), "project": inst.Project(), "err": err})
				return
			}

			// Send metrics for merging.
			metricsToMerge <- instanceMetrics
		}(inst)
	}

	// Merge results into fresh local metrics set.
	metrics := metrics.NewMetricSet(nil)

	for i := 0; i < instCount; i++ {
		instanceMetrics := <-metricsToMerge
		if instanceMetrics == nil {
			continue
		}

		metrics.Merge(instanceMetrics)
	}

	metricsStr := metrics.String()

	// Store freshly built metrics in cache.
	d.metricsMutex.Lock()
	d.metrics = metrics
	d.metricsLastBuildTime = time.Now()
	metricsGetBuilding = false
	d.metricsMutex.Unlock()

	return response.SyncResponsePlain(true, metricsStr)
}
