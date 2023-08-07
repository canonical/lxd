package main

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

type devLxdResponse struct {
	content any
	code    int
	ctype   string
}

// errorResponse creates a new *devLxdResponse with the provided status code and error message.
func errorResponse(code int, msg string) *devLxdResponse {
	return &devLxdResponse{msg, code, "raw"}
}

// okResponse creates a new *devLxdResponse with the provided content and content type, for a successful response.
func okResponse(ct any, ctype string) *devLxdResponse {
	return &devLxdResponse{ct, http.StatusOK, ctype}
}

// smartResponse creates a new *devLxdResponse based on the given error, handling various cases.
func smartResponse(err error) *devLxdResponse {
	if err == nil {
		return okResponse(nil, "")
	}

	statusCode, found := api.StatusErrorMatch(err)
	if found {
		return errorResponse(statusCode, err.Error())
	}

	return errorResponse(http.StatusInternalServerError, err.Error())
}
