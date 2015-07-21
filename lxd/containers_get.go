package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lxc/lxd/shared"
)

func containersGet(d *Daemon, r *http.Request) Response {
	for {
		result, err := doContainersGet(d, d.isRecursionRequest(r))
		if err == nil {
			return SyncResponse(true, result)
		}
		if !isDbLockedError(err) {
			shared.Debugf("DBERR: containersGet: error %q\n", err)
			return InternalError(err)
		}
		// 1 s may seem drastic, but we really don't want to thrash
		// perhaps we should use a random amount
		shared.Debugf("DBERR: containersGet, db is locked\n")
		shared.PrintStack()
		time.Sleep(1 * time.Second)
	}
}

func doContainersGetGoRoutine(
	d *Daemon, containers []string, channel chan []shared.ContainerInfo,
	wg *sync.WaitGroup) {

	myResults := []shared.ContainerInfo{}
	for _, name := range containers {
		container, response := doContainerGet(d, name)
		if response != nil {
			continue
		}

		myResults = append(myResults, container)
	}

	channel <- myResults

	wg.Done()
}

func doContainersGet(d *Daemon, recursion bool) (interface{}, error) {
	result, err := dbListContainers(d)
	if err != nil {
		return nil, err
	}

	resultString := []string{}
	resultMap := []shared.ContainerInfo{}
	resultLength := len(result)
	perRoutine := resultLength / 100 * 20
	routines := int(float64(resultLength)/float64(perRoutine) + 1.0)
	shared.Debugf("len %d float %f", resultLength, float64(resultLength)/float64(perRoutine)+1.0)

	if recursion {
		if resultLength > perRoutine {
			shared.Debugf("len %d routines %d", resultLength, routines)

			channel := make(chan []shared.ContainerInfo, perRoutine)
			wg := sync.WaitGroup{}

			for i := 0; i < routines; i++ {
				wg.Add(1)
				if i*perRoutine+perRoutine < resultLength {
					go doContainersGetGoRoutine(
						d,
						result[i*perRoutine:i*perRoutine+perRoutine],
						channel,
						&wg)
				} else {
					go doContainersGetGoRoutine(
						d,
						result[i*perRoutine:],
						channel,
						&wg)
				}
			}
			wg.Wait()

			for i := 0; i < routines; i++ {
				resultMap = append(resultMap, <-channel...)
			}

		} else {
			for _, container := range result {
				container, response := doContainerGet(d, container)
				if response != nil {
					continue
				}
				resultMap = append(resultMap, container)
			}
		}
	} else {
		for _, container := range result {
			url := fmt.Sprintf("/%s/containers/%s", shared.APIVersion, container)
			resultString = append(resultString, url)
		}
	}

	if !recursion {
		return resultString, nil
	}

	return resultMap, nil
}

func doContainerGet(d *Daemon, cname string) (shared.ContainerInfo, Response) {
	_, err := dbGetContainerID(d.db, cname)
	if err != nil {
		return shared.ContainerInfo{}, SmartError(err)
	}

	c, err := newLxdContainer(cname, d)
	if err != nil {
		return shared.ContainerInfo{}, SmartError(err)
	}

	var name string
	regexp := fmt.Sprintf("%s/", cname)
	length := len(regexp)
	q := "SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?"
	inargs := []interface{}{cTypeSnapshot, length, regexp}
	outfmt := []interface{}{name}
	results, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return shared.ContainerInfo{}, SmartError(err)
	}

	var body []string

	for _, r := range results {
		name = r[0].(string)

		url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", shared.APIVersion, cname, name)
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
