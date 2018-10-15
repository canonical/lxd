package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
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
	resultString := []string{}
	resultList := []*api.Container{}
	resultFullList := []*api.ContainerFull{}
	resultMu := sync.Mutex{}

	// Parse the recursion field
	recursionStr := r.FormValue("recursion")

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	// Parse the project field
	project := projectParam(r)

	// Get the list and location of all containers
	var result map[string][]string // Containers by node address
	var nodes map[string]string    // Node names by container
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		result, err = tx.ContainersListByNodeAddress(project)
		if err != nil {
			return err
		}

		nodes, err = tx.ContainersByNodeName(project)
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
	if recursion > 0 {
		cts, err := containerLoadNodeProjectAll(d.State(), project)
		if err != nil {
			return nil, err
		}

		for _, ct := range cts {
			nodeCts[ct.Name()] = ct
		}
	}

	// Append containers to list and handle errors
	resultListAppend := func(name string, c api.Container, err error) {
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

	resultFullListAppend := func(name string, c api.ContainerFull, err error) {
		if err != nil {
			c = api.ContainerFull{Container: api.Container{
				Name:       name,
				Status:     api.Error.String(),
				StatusCode: api.Error,
				Location:   nodes[name],
			}}
		}
		resultMu.Lock()
		resultFullList = append(resultFullList, &c)
		resultMu.Unlock()
	}

	// Get the data
	wg := sync.WaitGroup{}
	for address, containers := range result {
		// If this is an internal request from another cluster node,
		// ignore containers from other nodes, and return only the ones
		// on this node
		if isClusterNotification(r) && address != "" {
			continue
		}

		// Mark containers on unavailable nodes as down
		if recursion > 0 && address == "0.0.0.0" {
			for _, container := range containers {
				if recursion == 1 {
					resultListAppend(container, api.Container{}, fmt.Errorf("unavailable"))
				} else {
					resultFullListAppend(container, api.ContainerFull{}, fmt.Errorf("unavailable"))
				}
			}

			continue
		}

		// For recursion requests we need to fetch the state of remote
		// containers from their respective nodes.
		if recursion > 0 && address != "" && !isClusterNotification(r) {
			wg.Add(1)
			go func(address string, containers []string) {
				defer wg.Done()
				cert := d.endpoints.NetworkCert()

				if recursion == 1 {
					cs, err := doContainersGetFromNode(project, address, cert)
					if err != nil {
						for _, name := range containers {
							resultListAppend(name, api.Container{}, err)
						}

						return
					}

					for _, c := range cs {
						resultListAppend(c.Name, c, nil)
					}

					return
				}

				cs, err := doContainersFullGetFromNode(project, address, cert)
				if err != nil {
					for _, name := range containers {
						resultFullListAppend(name, api.ContainerFull{}, err)
					}

					return
				}

				for _, c := range cs {
					resultFullListAppend(c.Name, c, nil)
				}
			}(address, containers)

			continue
		}

		if recursion == 0 {
			for _, container := range containers {
				url := fmt.Sprintf("/%s/containers/%s", version.APIVersion, container)
				resultString = append(resultString, url)
			}
		} else {
			threads := 4
			if len(containers) < threads {
				threads = len(containers)
			}

			queue := make(chan string, threads)

			for i := 0; i < threads; i++ {
				wg.Add(1)

				go func() {
					for {
						container, more := <-queue
						if !more {
							break
						}

						if recursion == 1 {
							c, _, err := nodeCts[container].Render()
							if err != nil {
								resultListAppend(container, api.Container{}, err)
							} else {
								resultListAppend(container, *c.(*api.Container), err)
							}

							continue
						}

						c, _, err := nodeCts[container].RenderFull()
						if err != nil {
							resultFullListAppend(container, api.ContainerFull{}, err)
						} else {
							resultFullListAppend(container, *c, err)
						}
					}

					wg.Done()
				}()
			}

			for _, container := range containers {
				queue <- container
			}

			close(queue)
		}
	}
	wg.Wait()

	if recursion == 0 {
		return resultString, nil
	}

	if recursion == 1 {
		// Sort the result list by name.
		sort.Slice(resultList, func(i, j int) bool {
			return resultList[i].Name < resultList[j].Name
		})

		return resultList, nil
	}

	// Sort the result list by name.
	sort.Slice(resultFullList, func(i, j int) bool {
		return resultFullList[i].Name < resultFullList[j].Name
	})

	return resultFullList, nil
}

// Fetch information about the containers on the given remote node, using the
// rest API and with a timeout of 30 seconds.
func doContainersGetFromNode(project, node string, cert *shared.CertInfo) ([]api.Container, error) {
	f := func() ([]api.Container, error) {
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to node %s", node)
		}

		client = client.UseProject(project)

		containers, err := client.GetContainers()
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to get containers from node %s", node)
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
		err = fmt.Errorf("Timeout getting containers from node %s", node)
	case <-done:
	}

	return containers, err
}

func doContainersFullGetFromNode(project, node string, cert *shared.CertInfo) ([]api.ContainerFull, error) {
	f := func() ([]api.ContainerFull, error) {
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to node %s", node)
		}

		client = client.UseProject(project)

		containers, err := client.GetContainersFull()
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to get containers from node %s", node)
		}

		return containers, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var containers []api.ContainerFull
	var err error

	go func() {
		containers, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("Timeout getting containers from node %s", node)
	case <-done:
	}

	return containers, err
}
