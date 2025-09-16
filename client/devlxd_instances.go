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

// UpdateInstance updates an existing instance with the given name.
func (r *ProtocolDevLXD) UpdateInstance(instName string, inst api.DevLXDInstancePut, ETag string) error {
	url := api.NewURL().Path("instances", instName)
	_, _, err := r.query(http.MethodPatch, url.String(), inst, ETag)
	if err != nil {
		return err
	}

	return nil
}
