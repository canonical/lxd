package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetInstance retrieves the instance with the given name.
func (r *ProtocolDevLXD) GetInstance(instName string) (instance *api.DevLXDInstance, etag string, err error) {
	var inst api.DevLXDInstance

	url := api.NewURL().Path("instances", instName)
	etag, err = r.queryStruct(http.MethodGet, url.String(), nil, "", &inst)
	if err != nil {
		return nil, "", err
	}

	return &inst, etag, nil
}
