package request

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// ProjectParam returns the project query parameter from the given request or "default" if parameter is not set.
func ProjectParam(request *http.Request) string {
	projectParam := QueryParam(request, "project")
	if projectParam == "" {
		projectParam = api.ProjectDefaultName
	}

	return projectParam
}

// QueryParam extracts the given query parameter directly from the URL, never from an
// encoded body.
func QueryParam(request *http.Request, key string) string {
	var values url.Values
	var err error

	if request.URL != nil {
		values, err = url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			logger.Warnf("Failed to parse query string %q: %v", request.URL.RawQuery, err)
			return ""
		}
	}

	if values == nil {
		values = make(url.Values)
	}

	return values.Get(key)
}

// DecodeJSONBody decodes the JSON request body into the given struct.
func DecodeJSONBody(request *http.Request, to any) error {
	err := json.NewDecoder(request.Body).Decode(&to)
	if err != nil {
		return api.StatusErrorf(http.StatusBadRequest, "Failed to decode request body: %w", err)
	}

	return nil
}

// RestoreJSONBody restores the JSON body of the request to ensure it is not empty if it is read again.
// For example, when the  request is later forwarded to another cluster member.
func RestoreJSONBody(request *http.Request, from any) error {
	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(from)
	if err != nil {
		return err
	}

	request.Body = shared.BytesReadCloser{
		Buf: &buf,
	}

	return nil
}

// DecodeAndRestoreJSONBody decodes the JSON body into the provided value and restores the body
// so it can be read again later. For example, when the  request is later forwarded to another
// cluster member.
func DecodeAndRestoreJSONBody(request *http.Request, to any) error {
	var buf bytes.Buffer
	tee := io.TeeReader(request.Body, &buf)

	err := json.NewDecoder(tee).Decode(&to)
	if err != nil {
		return api.StatusErrorf(http.StatusBadRequest, "Failed to decode request body: %w", err)
	}

	request.Body = shared.BytesReadCloser{
		Buf: &buf,
	}

	return nil
}
