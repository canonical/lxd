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

	url := api.NewURL().Path("operations", url.PathEscape(uuid), "wait")
	url = url.WithQuery("timeout", strconv.FormatInt(int64(timeout), 10))

	etag, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}
