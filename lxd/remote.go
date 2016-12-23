package main

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

func remoteGetImageFingerprint(d *Daemon, server string, certificate string, alias string) (string, error) {
	url := fmt.Sprintf(
		"%s/%s/images/aliases/%s",
		server, version.APIVersion, alias)

	resp, err := d.httpGetSync(url, certificate)
	if err != nil {
		return "", err
	}

	var result api.ImageAliasesEntry
	if err = resp.MetadataAsStruct(&result); err != nil {
		return "", fmt.Errorf("Error reading alias")
	}

	return result.Target, nil
}
