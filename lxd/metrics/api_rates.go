package metrics

import (
	"net/url"
	"sync/atomic"

	"github.com/canonical/lxd/shared/entity"
)

// RequestResult represents a completed request status category.
type RequestResult int8

// This defines every possible request result to be used as a metric label.
const (
	ErrorServer RequestResult = iota
	ErrorClient
	Success
)

var requestResultNames = map[RequestResult]string{
	ErrorServer: "error_server",
	ErrorClient: "error_client",
	Success:     "succeeded",
}

// GetRequestResultsNames returns a map containing all possible request result types and their names.
// This is also used to iterate through the possible results.
func GetRequestResultsNames() map[RequestResult]string {
	return requestResultNames
}

type completedMetricsLabeling struct {
	entityType entity.Type
	result     RequestResult
}

var ongoingRequests map[entity.Type]*atomic.Int64
var completedRequests map[completedMetricsLabeling]*atomic.Int64

// InitAPIMetrics initializes maps with initial values for the API rates metrics.
func InitAPIMetrics() {
	relevantEntityTypes := entity.APIMetricsEntityTypes()
	ongoingRequests = make(map[entity.Type]*atomic.Int64, len(relevantEntityTypes))
	completedRequests = make(map[completedMetricsLabeling]*atomic.Int64, len(relevantEntityTypes)*len(requestResultNames))

	for _, entityType := range relevantEntityTypes {
		ongoingRequests[entityType] = new(atomic.Int64)
		for result := range requestResultNames {
			completedRequests[completedMetricsLabeling{entityType: entityType, result: result}] = new(atomic.Int64)
		}
	}
}

// TrackStartedRequest should be called before each request handler to keep track of ongoing requests.
func TrackStartedRequest(url url.URL) {
	ongoingRequests[entity.EndpointEntityType(url)].Add(1)
}

// TrackCompletedRequest should be called after each request is completed to keep track of completed requests.
func TrackCompletedRequest(url url.URL, result RequestResult) {
	entityType := entity.EndpointEntityType(url)
	ongoingRequests[entityType].Add(-1)
	completedRequests[completedMetricsLabeling{entityType: entityType, result: result}].Add(1)
}

// GetOngoingRequests gets the value for ongoing metrics filtered by entity type.
func GetOngoingRequests(entityType entity.Type) int64 {
	return ongoingRequests[entityType].Load()
}

// GetCompletedRequests gets the value of completed requests filtered by entity type and result.
func GetCompletedRequests(entityType entity.Type, result RequestResult) int64 {
	return completedRequests[completedMetricsLabeling{entityType: entityType, result: result}].Load()
}
