package main

import (
	"encoding/json"

	"github.com/lxc/lxd/shared/idmap"
)

func idmapsetToJSON(idmapSet *idmap.IdmapSet) (string, error) {
	idmapBytes, err := json.Marshal(idmapSet.Idmap)
	if err != nil {
		return "", err
	}

	return string(idmapBytes), nil
}
