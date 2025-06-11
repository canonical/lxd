package lxd

import (
	"encoding/json"
	"net/http"
)

// GetMetadata retrieves the instance's meta-data.
func (r *ProtocolDevLXD) GetMetadata() (metadata string, err error) {
	resp, _, err := r.query(http.MethodGet, "/meta-data", nil, "")
	if err != nil {
		return "", err
	}

	if r.isDevLXDOverVsock {
		var metadata string

		// The returned string value is JSON encoded.
		err = json.Unmarshal(resp.Content, &metadata)
		if err != nil {
			return "", err
		}

		return metadata, nil
	}

	return string(resp.Content), nil
}
