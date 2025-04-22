package lxd

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/api"
)

// GetOperationUUIDs returns a list of operation uuids.
func (r *ProtocolLXD) GetOperationUUIDs() ([]string, error) {
	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/operations"
	_, err := r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetOperations returns a list of Operation struct.
func (r *ProtocolLXD) GetOperations() ([]api.Operation, error) {
	apiOperations := map[string][]api.Operation{}

	// Fetch the raw value.
	_, err := r.queryStruct(http.MethodGet, "/operations?recursion=1", nil, "", &apiOperations)
	if err != nil {
		return nil, err
	}

	// Turn it into a list of operations.
	operations := []api.Operation{}
	for _, v := range apiOperations {
		operations = append(operations, v...)
	}

	return operations, nil
}

// GetOperationsAllProjects returns a list of operations from all projects.
func (r *ProtocolLXD) GetOperationsAllProjects() ([]api.Operation, error) {
	err := r.CheckExtension("operations_get_query_all_projects")
	if err != nil {
		return nil, err
	}

	apiOperations := map[string][]api.Operation{}

	path := "/operations"

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("all-projects", "true")

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, path+"?"+v.Encode(), nil, "", &apiOperations)
	if err != nil {
		return nil, err
	}

	// Turn it into a list of operations.
	operations := []api.Operation{}
	for _, v := range apiOperations {
		operations = append(operations, v...)
	}

	return operations, nil
}

// GetOperation returns an Operation entry for the provided uuid.
func (r *ProtocolLXD) GetOperation(uuid string) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct(http.MethodGet, "/operations/"+url.PathEscape(uuid), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWait returns an Operation entry for the provided uuid once it's complete or hits the timeout.
func (r *ProtocolLXD) GetOperationWait(uuid string, timeout int) (*api.Operation, string, error) {
	op := api.Operation{}

	// Unset the response header timeout so that the request does not time out.
	transport, err := r.getUnderlyingHTTPTransport()
	if err != nil {
		return nil, "", err
	}

	transport.ResponseHeaderTimeout = 0

	// Fetch the raw value
	etag, err := r.queryStruct(http.MethodGet, "/operations/"+url.PathEscape(uuid)+"/wait?timeout="+strconv.FormatInt(int64(timeout), 10), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWaitSecret returns an Operation entry for the provided uuid and secret once it's complete or hits the timeout.
func (r *ProtocolLXD) GetOperationWaitSecret(uuid string, secret string, timeout int) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct(http.MethodGet, "/operations/"+url.PathEscape(uuid)+"/wait?secret="+url.PathEscape(secret)+"&timeout="+strconv.FormatInt(int64(timeout), 10), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWebsocket returns a websocket connection for the provided operation.
func (r *ProtocolLXD) GetOperationWebsocket(uuid string, secret string) (*websocket.Conn, error) {
	path := "/operations/" + url.PathEscape(uuid) + "/websocket"
	if secret != "" {
		path += "?secret=" + url.QueryEscape(secret)
	}

	return r.websocket(path)
}

// DeleteOperation deletes (cancels) a running operation.
func (r *ProtocolLXD) DeleteOperation(uuid string) error {
	// Send the request
	_, _, err := r.query(http.MethodDelete, "/operations/"+url.PathEscape(uuid), nil, "")
	if err != nil {
		return err
	}

	return nil
}
