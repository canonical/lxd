package main

import (
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
)

// Render renders the response and returns a potential error.
func Render(req *http.Request, resp response.Response) error {
	rc := response.NewResponseCapture(req)
	err := resp.Render(rc, req)
	if err != nil {
		return err
	}

	_, _, err = rc.ToAPIResponse()
	if err != nil {
		return err
	}

	return nil
}

// RenderToStruct renders the response into a struct and returns the ETag.
func RenderToStruct(req *http.Request, resp response.Response, target any) (etag string, err error) {
	rc := response.NewResponseCapture(req)
	err = resp.Render(rc, req)
	if err != nil {
		return "", err
	}

	apiResp, etag, err := rc.ToAPIResponse()
	if err != nil {
		return "", err
	}

	err = apiResp.MetadataAsStruct(target)
	if err != nil {
		return "", err
	}

	return etag, nil
}

// RenderToOperation renders the response into an operation and returns the ETag.
func RenderToOperation(req *http.Request, resp response.Response) (operation *api.Operation, err error) {
	rc := response.NewResponseCapture(req)
	err = resp.Render(rc, req)
	if err != nil {
		return nil, err
	}

	apiResp, _, err := rc.ToAPIResponse()
	if err != nil {
		return nil, err
	}

	// Get the operation from metadata.
	op, err := apiResp.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	return op, nil
}
