package main

import (
	"net/http"

	"github.com/canonical/lxd/lxd/response"
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
