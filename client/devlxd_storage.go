package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetStoragePool retrieves the storage pool with a given name.
func (r *ProtocolDevLXD) GetStoragePool(poolName string) (*api.DevLXDStoragePool, string, error) {
	var pool api.DevLXDStoragePool

	url := api.NewURL().Path("storage-pools", poolName).URL
	etag, err := r.queryStruct(http.MethodGet, url.String(), nil, "", &pool)
	if err != nil {
		return nil, "", err
	}

	return &pool, etag, nil
}
