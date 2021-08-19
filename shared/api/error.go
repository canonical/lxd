package api

import (
	"fmt"
	"net/http"
)

// StatusErrorf returns a new StatusError containing the specified status and message.
func StatusErrorf(status int, format string, a ...interface{}) *StatusError {
	return &StatusError{
		status: status,
		msg:    fmt.Sprintf(format, a...),
	}
}

// StatusError error type that contains an HTTP status code and message.
type StatusError struct {
	status int
	msg    string
}

// Error returns the error message or the http.StatusText() of the status code if message is empty.
func (e *StatusError) Error() string {
	if e.msg != "" {
		return e.msg
	}

	return http.StatusText(e.status)
}

// Status returns the HTTP status code.
func (e *StatusError) Status() int {
	return e.status
}
