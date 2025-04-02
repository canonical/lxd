package lxd

import (
	"net/http"
)

// GetDevices retrieves a map of guest's devices.
func (r *ProtocolDevLXD) GetDevices() (devices map[string]map[string]string, err error) {
	devices = make(map[string]map[string]string)

	_, err = r.queryStruct(http.MethodGet, "/devices", nil, "", &devices)
	if err != nil {
		return nil, err
	}

	return devices, nil
}
