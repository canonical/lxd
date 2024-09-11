package metrics

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
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

// countStartedRequest should be called before each request handler to keep track of ongoing requests.
func countStartedRequest(url url.URL) {
	ongoingRequests[entity.EndpointEntityType(url)].Add(1)
}

// countCompletedRequest should be called after each request is completed to keep track of completed requests.
func countCompletedRequest(url url.URL, result RequestResult) {
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

// TrackStartedRequest tracks the request as started for the API metrics and
// injects a callback function to track the request as completed.
func TrackStartedRequest(r *http.Request) {
	requestURL := *r.URL

	// Set the callback function to track the request as completed.
	// Use sync.Once to ensure it can be called at most once.
	var once sync.Once
	callbackFunc := func(result RequestResult) {
		once.Do(func() {
			countCompletedRequest(requestURL, result)
		})
	}

	request.SetCtxValue(r, request.MetricsCallbackFunc, callbackFunc)

	countStartedRequest(requestURL)
}

// UseMetricsCallback retrieves a callback function from the request context and calls it.
// The callback function is used to mark the request as completed for the API metrics.
func UseMetricsCallback(req *http.Request, result RequestResult) {
	callback, err := request.GetCtxValue[func(RequestResult)](req.Context(), request.MetricsCallbackFunc)
	if err != nil && (strings.HasPrefix(req.URL.Path, "/1.0") || req.URL.Path == "/") {
		// Log a warning if endpoint is part of the main API, and therefore should be counted fot the API metrics.
		logger.Warn("Request will not be counted for the API metrics", logger.Ctx{"url": req.URL.Path, "method": req.Method, "remote": req.RemoteAddr})
	} else if err == nil && callback != nil {
		callback(result)
	}
}
