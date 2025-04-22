package lxd

import (
	"net/http"
)

// GetMetadata retrieves the instance's meta-data.
func (r *ProtocolDevLXD) GetMetadata() (metadata string, err error) {
	resp, _, err := r.query(http.MethodGet, "/meta-data", nil, "")
	if err != nil {
		return "", err
	}

	return string(resp.Content), nil
}
