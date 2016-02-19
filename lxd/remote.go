package main

import (
	"encoding/json"
	"fmt"

	"github.com/lxc/lxd/shared"
)

func remoteGetImageFingerprint(d *Daemon, server string, certificate string, alias string) (string, error) {
	url := fmt.Sprintf(
		"%s/%s/images/aliases/%s",
		server, shared.APIVersion, alias)

	resp, err := d.httpGetSync(url, certificate)
	if err != nil {
		return "", err
	}

	var result shared.ImageAliasesEntry
	if err = json.Unmarshal(resp.Metadata, &result); err != nil {
		return "", fmt.Errorf("Error reading alias")
	}

	return result.Target, nil
}
