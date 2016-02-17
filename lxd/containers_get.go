package main

import (
	"fmt"
	"net/http"
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
	resultList := []*shared.ContainerInfo{}
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
			resultList = append(resultList, container)
		}
	}

	if !recursion {
		return resultString, nil
	}

	return resultList, nil
}

func doContainerGet(d *Daemon, cname string) (*shared.ContainerInfo, Response) {
	c, err := containerLoadByName(d, cname)
	if err != nil {
		return nil, SmartError(err)
	}

	cts, err := c.Render()
	if err != nil {
		return nil, SmartError(err)
	}

	return cts, nil
}
