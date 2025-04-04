package main

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

type devLXDResponse struct {
	content any
	code    int
	ctype   string
}

func errorResponse(code int, msg string) *devLXDResponse {
	return &devLXDResponse{msg, code, "raw"}
}

func okResponse(ct any, ctype string) *devLXDResponse {
	return &devLXDResponse{ct, http.StatusOK, ctype}
}

func smartResponse(err error) *devLXDResponse {
	if err == nil {
		return okResponse(nil, "")
	}

	statusCode, found := api.StatusErrorMatch(err)
	if found {
		return errorResponse(statusCode, err.Error())
	}

	return errorResponse(http.StatusInternalServerError, err.Error())
}
