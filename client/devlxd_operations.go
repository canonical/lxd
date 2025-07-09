package lxd

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/canonical/lxd/shared/api"
)

// GetOperationWait returns a DevLXDOperation entry for the provided uuid once it's complete or hits the timeout.
func (r *ProtocolDevLXD) GetOperationWait(uuid string, timeout int) (*api.DevLXDOperation, string, error) {
	var op api.DevLXDOperation

	etag, err := r.queryStruct(http.MethodGet, "/operations/"+url.PathEscape(uuid)+"/wait?timeout="+strconv.FormatInt(int64(timeout), 10), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}
