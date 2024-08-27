package api

import (
	"errors"
	"fmt"
	"net/http"
)

// NewGenericStatusError returns a new StatusError with the given status code.
// The generic http.StatusText will be used for the error message.
func NewGenericStatusError(status int) StatusError {
	return StatusError{
		status: status,
	}
}

// NewStatusError returns a new StatusError with the given message and status code.
func NewStatusError(status int, msg string) StatusError {
	if msg == "" {
		return NewGenericStatusError(status)
	}

	return StatusError{
		status: status,
		err:    errors.New(msg),
	}
}

// StatusErrorf returns a new StatusError containing the specified status and message.
func StatusErrorf(status int, format string, a ...any) StatusError {
	return StatusError{
		status: status,
		err:    fmt.Errorf(format, a...),
	}
}

// StatusError error type that contains an HTTP status code and message.
type StatusError struct {
	status int
	err    error
}

// Error returns the error message or the http.StatusText() of the status code if error message is empty.
func (e StatusError) Error() string {
	if e.err != nil && e.err.Error() != "" {
		return e.err.Error()
	}

	statusText := http.StatusText(e.status)
	if statusText == "" {
		return "Undefined error"
	}

	return statusText
}

// Unwrap implements the xerrors.Wrapper interface for StatusError.
func (e StatusError) Unwrap() error {
	return e.err
}

// Status returns the HTTP status code.
func (e StatusError) Status() int {
	return e.status
}

// StatusErrorMatch checks if err was caused by StatusError. Can optionally also check whether the StatusError's
// status code matches one of the supplied status codes in matchStatus.
// Returns the matched StatusError status code and true if match criteria are met, otherwise false.
func StatusErrorMatch(err error, matchStatusCodes ...int) (int, bool) {
	var statusErr StatusError

	if errors.As(err, &statusErr) {
		statusCode := statusErr.Status()

		if len(matchStatusCodes) <= 0 {
			return statusCode, true
		}

		for _, s := range matchStatusCodes {
			if statusCode == s {
				return statusCode, true
			}
		}
	}

	return -1, false
}

// StatusErrorCheck returns whether or not err was caused by a StatusError and if it matches one of the
// optional status codes.
func StatusErrorCheck(err error, matchStatusCodes ...int) bool {
	_, found := StatusErrorMatch(err, matchStatusCodes...)
	return found
}
