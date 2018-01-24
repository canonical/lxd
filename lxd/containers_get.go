package main

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

func containersGet(d *Daemon, r *http.Request) Response {
	for i := 0; i < 100; i++ {
		result, err := doContainersGet(d, r)
		if err == nil {
			return SyncResponse(true, result)
		}
		if !query.IsRetriableError(err) {
			logger.Debugf("DBERR: containersGet: error %q", err)
			return SmartError(err)
		}
		// 1 s may seem drastic, but we really don't want to thrash
		// perhaps we should use a random amount
		time.Sleep(100 * time.Millisecond)
	}

	logger.Debugf("DBERR: containersGet, db is locked")
	logger.Debugf(logger.GetStack())
	return InternalError(fmt.Errorf("DB is locked"))
}

func doContainersGet(d *Daemon, r *http.Request) (interface{}, error) {
	var result map[string][]string
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		result, err = tx.ContainersListByNodeAddress()
		return err
	})
	if err != nil {
		return []string{}, err
	}

	recursion := util.IsRecursionRequest(r)
	resultString := []string{}
	resultList := []*api.Container{}
	resultMu := sync.Mutex{}

	resultAppend := func(name string, c api.Container, err error) {
		if err != nil {
			c = api.Container{
				Name:       name,
				Status:     api.Error.String(),
				StatusCode: api.Error}
		}
		resultMu.Lock()
		resultList = append(resultList, &c)
		resultMu.Unlock()
	}

	wg := sync.WaitGroup{}
	for address, containers := range result {
		// Mark containers on unavailable nodes as down
		if recursion && address == "0.0.0.0" {
			for _, container := range containers {
				resultAppend(container, api.Container{}, fmt.Errorf("unavailable"))
			}
		}

		// If this is an internal request from another cluster node,
		// ignore containers from other nodes, and return only the ones
		// on this node
		if isClusterNotification(r) && address != "" {
			continue
		}

		// For recursion requests we need to fetch the state of remote
		// containers from their respective nodes.
		if recursion && address != "" && !isClusterNotification(r) {
			wg.Add(1)
			go func(address string) {
				cert := d.endpoints.NetworkCert()
				cs, err := doContainersGetFromNode(address, cert)
				for _, c := range cs {
					resultAppend(c.Name, c, err)
				}
				wg.Done()
			}(address)
			continue
		}

		for _, container := range containers {
			if !recursion {
				url := fmt.Sprintf("/%s/containers/%s", version.APIVersion, container)
				resultString = append(resultString, url)
				continue
			}

			c, err := doContainerGet(d.State(), container)
			resultAppend(container, *c, err)
		}
	}
	wg.Wait()

	if !recursion {
		return resultString, nil
	}

	// Sort the result list by name.
	sort.Slice(resultList, func(i, j int) bool {
		return resultList[i].Name < resultList[j].Name
	})

	return resultList, nil
}

func doContainerGet(s *state.State, cname string) (*api.Container, error) {
	c, err := containerLoadByName(s, cname)
	if err != nil {
		return nil, err
	}

	cts, _, err := c.Render()
	if err != nil {
		return nil, err
	}

	return cts.(*api.Container), nil
}

// Fetch information about the containers on the given remote node, using the
// rest API and with a timeout of 30 seconds.
func doContainersGetFromNode(node string, cert *shared.CertInfo) ([]api.Container, error) {
	f := func() ([]api.Container, error) {
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to connect to node %s", node)
		}
		containers, err := client.GetContainers()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get containers from node %s", node)
		}
		return containers, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var containers []api.Container
	var err error

	go func() {
		containers, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("timeout getting containers from node %s", node)
	case <-done:
	}

	return containers, err
}
