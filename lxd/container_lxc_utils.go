package main

import (
	"encoding/json"

	"github.com/lxc/lxd/shared/idmap"
)

func idmapsetFromString(idmapString string) (*idmap.IdmapSet, error) {
	lastIdmap := new(idmap.IdmapSet)
	err := json.Unmarshal([]byte(idmapString), &lastIdmap.Idmap)
	if err != nil {
		return nil, err
	}

	if len(lastIdmap.Idmap) == 0 {
		return nil, nil
	}

	return lastIdmap, nil
}

func idmapsetToJSON(idmapSet *idmap.IdmapSet) (string, error) {
	idmapBytes, err := json.Marshal(idmapSet.Idmap)
	if err != nil {
		return "", err
	}

	return string(idmapBytes), nil
}
