package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetState retrieves the guest state.
func (r *ProtocolDevLXD) GetState() (*api.DevLXDGet, error) {
	var info api.DevLXDGet

	_, err := r.queryStruct(http.MethodGet, "", nil, "", &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

// UpdateState updates the guest state.
func (r *ProtocolDevLXD) UpdateState(req api.DevLXDPut) error {
	_, _, err := r.query(http.MethodPatch, "", req, "")
	if err != nil {
		return err
	}

	return nil
}
