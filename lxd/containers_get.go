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
	recursion := util.IsRecursionRequest(r)
	resultString := []string{}
	resultList := []*api.Container{}
	resultMu := sync.Mutex{}

	// Get the list and location of all containers
	var result map[string][]string // Containers by node address
	var nodes map[string]string    // Node names by container
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		result, err = tx.ContainersListByNodeAddress()
		if err != nil {
			return err
		}

		nodes, err = tx.ContainersByNodeName()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return []string{}, err
	}

	// Get the local containers
	nodeCts := map[string]container{}
	if recursion {
		cts, err := containerLoadNodeAll(d.State())
		if err != nil {
			return nil, err
		}

		for _, ct := range cts {
			nodeCts[ct.Name()] = ct
		}
	}

	resultAppend := func(name string, c api.Container, err error) {
		if err != nil {
			c = api.Container{
				Name:       name,
				Status:     api.Error.String(),
				StatusCode: api.Error,
				Location:   nodes[name],
			}
		}
		resultMu.Lock()
		resultList = append(resultList, &c)
		resultMu.Unlock()
	}

	wg := sync.WaitGroup{}
	for address, containers := range result {
		// If this is an internal request from another cluster node,
		// ignore containers from other nodes, and return only the ones
		// on this node
		if isClusterNotification(r) && address != "" {
			continue
		}

		// Mark containers on unavailable nodes as down
		if recursion && address == "0.0.0.0" {
			for _, container := range containers {
				resultAppend(container, api.Container{}, fmt.Errorf("unavailable"))
			}

			continue
		}

		// For recursion requests we need to fetch the state of remote
		// containers from their respective nodes.
		if recursion && address != "" && !isClusterNotification(r) {
			wg.Add(1)
			go func(address string, containers []string) {
				defer wg.Done()
				cert := d.endpoints.NetworkCert()

				cs, err := doContainersGetFromNode(address, cert)
				if err != nil {
					for _, name := range containers {
						resultAppend(name, api.Container{}, err)
					}

					return
				}

				for _, c := range cs {
					resultAppend(c.Name, c, nil)
				}
			}(address, containers)

			continue
		}

		for _, container := range containers {
			if !recursion {
				url := fmt.Sprintf("/%s/containers/%s", version.APIVersion, container)
				resultString = append(resultString, url)
				continue
			}

			c, _, err := nodeCts[container].Render()
			if err != nil {
				resultAppend(container, api.Container{}, err)
			} else {
				resultAppend(container, *c.(*api.Container), err)
			}
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
