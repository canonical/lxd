package main

import (
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
)

type devLXDResponse struct {
	content any
	code    int
	ctype   string
	etag    string
	hook    func(w http.ResponseWriter) error
}

// Render renders a devLXD response.
func (r *devLXDResponse) Render(w http.ResponseWriter, req *http.Request) error {
	var err error

	// Handle hooks first, if defined.
	if r.hook != nil {
		return r.hook(w)
	}

	// Handle error responses.
	if r.code != http.StatusOK {
		http.Error(w, fmt.Sprint(r.content), r.code)
		return nil
	}

	// Set ETag if provided.
	if r.etag != "" {
		w.Header().Set("Etag", r.etag)
	}

	// Handle different content types.
	if r.ctype == "json" {
		w.Header().Set("Content-Type", "application/json")
		err = util.WriteJSON(w, r.content, nil)
	} else if r.ctype != "websocket" {
		w.Header().Set("Content-Type", "application/octet-stream")
		if r.content != nil {
			_, err = fmt.Fprint(w, fmt.Sprint(r.content))
		}
	}

	return err
}

func (r *devLXDResponse) String() string {
	if r.hook != nil {
		return "unknown"
	}

	if r.code == http.StatusOK {
		return "success"
	}

	return "failure"
}

func errorResponse(code int, msg string) *devLXDResponse {
	return &devLXDResponse{
		content: msg,
		code:    code,
		ctype:   "raw",
	}
}

func okResponse(ct any, ctype string) *devLXDResponse {
	return &devLXDResponse{
		content: ct,
		code:    http.StatusOK,
		ctype:   ctype,
	}
}

func okResponseETag(ct any, ctype string, etag string) *devLXDResponse {
	return &devLXDResponse{
		content: ct,
		code:    http.StatusOK,
		ctype:   ctype,
		etag:    etag,
	}
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

// manualResponse returns the devLXDResponse with a configured hook. The hook is
// executed when response is rendered.
func manualResponse(hook func(w http.ResponseWriter) error) *devLXDResponse {
	return &devLXDResponse{hook: hook}
}
