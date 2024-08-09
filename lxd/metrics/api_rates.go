package metrics

import (
	"net/url"
	"sync/atomic"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// RequestResult represents a completed request status category.
type RequestResult string

const (
	errorServer RequestResult = "error_server"
	errorClient RequestResult = "error_client"
	success     RequestResult = "succeeded"
)

// requestResultsTypes contains all the possible request result categories.
var requestResultsTypes = []RequestResult{
	errorServer,
	errorClient,
	success,
}

// GetRequestResultsTypes returns all possible request result types.
func GetRequestResultsTypes() []RequestResult {
	return requestResultsTypes
}

// ResolveResponseStatus categorizes a status code from a http response as serverError, clientError or success.
func ResolveResponseStatus(statusCode int) RequestResult {
	// 4* codes are considered client errors on HTTP.
	if statusCode >= 400 && statusCode < 500 {
		return errorClient
	}

	// 2* codes are considered successes on HTTP, 300 are for forwarded requests
	// that are included as successes for the purpose of the metrics.
	if statusCode >= 200 && statusCode < 400 {
		return success
	}

	// Any other status code is considered a server error.
	return errorServer
}

// ResolveOperationStatus categorizes a status code from an operation as serverError, clientError or success.
func ResolveOperationStatus(statusCode api.StatusCode) RequestResult {
	// If the operation was not completed as expected an neither cancelled by an user, it is considered a failure.
	if statusCode != api.Success && statusCode != api.Cancelled {
		return errorServer
	}

	// Otherwise we consider it a success.
	return success
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
	completedRequests = make(map[completedMetricsLabeling]*atomic.Int64, len(relevantEntityTypes)*len(requestResultsTypes))

	for _, entityType := range relevantEntityTypes {
		ongoingRequests[entityType] = new(atomic.Int64)
		for _, result := range requestResultsTypes {
			completedRequests[completedMetricsLabeling{entityType: entityType, result: result}] = new(atomic.Int64)
		}
	}
}

// TrackStartedRequest is used as a middleware before every request handler to keep track of ongoing requests.
func TrackStartedRequest(url url.URL) {
	ongoingRequests[entity.EndpointEntityType(url)].Add(1)
}

// TrackCompletedRequest should be called after each request is completed to keep track of completed requests.
func TrackCompletedRequest(url url.URL, result RequestResult) {
	entityType := entity.EndpointEntityType(url)
	ongoingRequests[entity.EndpointEntityType(url)].Add(-1)
	completedRequests[completedMetricsLabeling{entityType: entityType, result: result}].Add(1)
}

// GetOngoingRequests gets the value for ongoing metrics filtered by entityType.
func GetOngoingRequests(entityType entity.Type) int64 {
	return ongoingRequests[entityType].Load()
}

// GetCompletedRequests gets the value of completed requests filtered by entityType and result.
func GetCompletedRequests(entityType entity.Type, result RequestResult) int64 {
	return completedRequests[completedMetricsLabeling{entityType: entityType, result: result}].Load()
}
