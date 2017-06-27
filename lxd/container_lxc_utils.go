package main

import (
	"encoding/json"

	"github.com/lxc/lxd/shared"
)

func idmapsetFromString(idmap string) (*shared.IdmapSet, error) {
	lastIdmap := new(shared.IdmapSet)
	err := json.Unmarshal([]byte(idmap), &lastIdmap.Idmap)
	if err != nil {
		return nil, err
	}

	if len(lastIdmap.Idmap) == 0 {
		return nil, nil
	}

	return lastIdmap, nil
}

func idmapsetToJSON(idmap *shared.IdmapSet) (string, error) {
	idmapBytes, err := json.Marshal(idmap.Idmap)
	if err != nil {
		return "", err
	}

	return string(idmapBytes), nil
}
