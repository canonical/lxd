package response

import (
	"database/sql"
	"errors"
	"net/http"
	"os"

	"github.com/canonical/lxd/shared/api"
)

// sentinelErrors contains some default mappings of HTTP status codes to sentinel errors that are defined in the Go
// standard library. Additional errors can be added to this map on a call to Init.
var sentinelErrors = map[int][]error{
	http.StatusNotFound:  {os.ErrNotExist, sql.ErrNoRows},
	http.StatusForbidden: {os.ErrPermission},
}

// checkSentinelErrors checks all sentinel errors in sentinelErrors and returns the appropriate code if a match is found.
// Otherwise, it returns zero (which is not a valid HTTP status code, and so indicates that there was no match).
// Additionally, checkSentinelErrors returns a string message which overwrites the API error message contents if non-empty.
func checkSentinelErrors(err error) (code int, msg string) {
	for code, errList := range sentinelErrors {
		for _, sentinelErr := range errList {
			if err == sentinelErr {
				// If the error has not been wrapped, return the generic HTTP status text.
				return code, http.StatusText(code)
			}

			if errors.Is(err, sentinelErr) {
				// If the error has been wrapped, return an empty message to indicate that the message of the original error should be used.
				return code, ""
			}
		}
	}

	return 0, ""
}

// errFuncs is used by getCodeAndMessage to determine the HTTP status code and message of an error. This list can be appended to via Init.
var errFuncs = []func(error) (int, string){
	checkSentinelErrors,
}

// SmartError returns a Response with a code and message extracted from the given error.
func SmartError(err error) Response {
	code, msg := getCodeAndMessage(err)
	if code == http.StatusOK {
		return EmptySyncResponse
	}

	if msg != "" {
		return &errorResponse{code: code, err: errors.New(msg)}
	}

	return &errorResponse{code: code, err: err}
}

// getCodeAndMessage gets HTTP response code and message for a given error, or returns [http.StatusInternalServerError].
// If the returned message is empty, SmartError will use the contents of err.Error().
func getCodeAndMessage(err error) (int, string) {
	// If no error, return 200 OK.
	if err == nil {
		return http.StatusOK, ""
	}

	// Check if it is an [api.StatusError] and if so, return the associated code.
	statusCode, found := api.StatusErrorMatch(err)
	if found {
		return statusCode, ""
	}

	// Iterate over errFuncs, if the code has valid StatusText, then use that.
	for _, f := range errFuncs {
		code, msg := f(err)
		if http.StatusText(code) == "" {
			continue
		}

		return code, msg
	}

	// Otherwise return 500 Internal Server Error.
	return http.StatusInternalServerError, ""
}

// IsNotFoundError returns true if the error is considered a Not Found error.
func IsNotFoundError(err error) bool {
	code, _ := getCodeAndMessage(err)
	return code == http.StatusNotFound
}
