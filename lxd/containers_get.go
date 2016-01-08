package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
)

func containersGet(d *Daemon, r *http.Request) Response {
	for i := 0; i < 100; i++ {
		result, err := doContainersGet(d, d.isRecursionRequest(r))
		if err == nil {
			return SyncResponse(true, result)
		}
		if !isDbLockedError(err) {
			shared.Debugf("DBERR: containersGet: error %q", err)
			return InternalError(err)
		}
		// 1 s may seem drastic, but we really don't want to thrash
		// perhaps we should use a random amount
		time.Sleep(100 * time.Millisecond)
	}

	shared.Debugf("DBERR: containersGet, db is locked")
	shared.PrintStack()
	return InternalError(fmt.Errorf("DB is locked"))
}

func doContainersGet(d *Daemon, recursion bool) (interface{}, error) {
	result, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return nil, err
	}

	resultString := []string{}
	resultMap := shared.ContainerInfoList{}
	if err != nil {
		return []string{}, err
	}
	for _, container := range result {
		if !recursion {
			url := fmt.Sprintf("/%s/containers/%s", shared.APIVersion, container)
			resultString = append(resultString, url)
		} else {
			container, response := doContainerGet(d, container)
			if response != nil {
				continue
			}
			resultMap = append(resultMap, container)
		}
	}

	if !recursion {
		return resultString, nil
	}

	return resultMap, nil
}

func doContainerGet(d *Daemon, cname string) (shared.ContainerInfo, Response) {
	c, err := containerLoadByName(d, cname)
	if err != nil {
		return shared.ContainerInfo{}, SmartError(err)
	}

	results, err := dbContainerGetSnapshots(d.db, cname)
	if err != nil {
		return shared.ContainerInfo{}, SmartError(err)
	}

	var body []string

	for _, name := range results {
		url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", shared.APIVersion, cname, strings.SplitN(name, shared.SnapshotDelimiter, 2)[1])
		body = append(body, url)
	}

	cts, err := c.RenderState()
	if err != nil {
		return shared.ContainerInfo{}, SmartError(err)
	}

	containerinfo := shared.ContainerInfo{State: *cts,
		Snaps: body}

	return containerinfo, nil
}
