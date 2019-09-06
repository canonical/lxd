package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/instance"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

func instancesGet(d *Daemon, r *http.Request) Response {
	for i := 0; i < 100; i++ {
		result, err := doInstancesGet(d, r)
		if err == nil {
			return SyncResponse(true, result)
		}
		if !query.IsRetriableError(err) {
			logger.Debugf("DBERR: containersGet: error %q", err)
			return SmartError(err)
		}
		// 100 ms may seem drastic, but we really don't want to thrash
		// perhaps we should use a random amount
		time.Sleep(100 * time.Millisecond)
	}

	logger.Debugf("DBERR: instancesGet, db is locked")
	logger.Debugf(logger.GetStack())
	return InternalError(fmt.Errorf("DB is locked"))
}

func doInstancesGet(d *Daemon, r *http.Request) (interface{}, error) {
	instanceType := instance.TypeAny
	if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
		instanceType = instance.TypeContainer
	}

	resultString := []string{}
	resultList := []*api.Instance{}
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
	var result map[string][]string // Instances by node address
	var nodes map[string]string    // Node names by instance
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		result, err = tx.InstancesListByNodeAddress(project, instanceType)
		if err != nil {
			return err
		}

		nodes, err = tx.InstancesByNodeName(project, instanceType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return []string{}, err
	}

	// Get the local instances
	nodeCts := map[string]container{}
	if recursion > 0 {
		cts, err := instanceLoadNodeProjectAll(d.State(), project, instanceType)
		if err != nil {
			return nil, err
		}

		for _, ct := range cts {
			nodeCts[ct.Name()] = ct
		}
	}

	// Append instances to list and handle errors
	resultListAppend := func(name string, c api.Instance, err error) {
		if err != nil {
			c = api.Instance{
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
	for address, instances := range result {
		// If this is an internal request from another cluster node,
		// ignore instances from other nodes, and return only the ones
		// on this node
		if isClusterNotification(r) && address != "" {
			continue
		}

		// Mark instances on unavailable nodes as down
		if recursion > 0 && address == "0.0.0.0" {
			for _, instanceName := range instances {
				if recursion == 1 {
					resultListAppend(instanceName, api.Instance{}, fmt.Errorf("unavailable"))
				} else {
					resultFullListAppend(instanceName, api.ContainerFull{}, fmt.Errorf("unavailable"))
				}
			}

			continue
		}

		// For recursion requests we need to fetch the state of remote
		// instances from their respective nodes.
		if recursion > 0 && address != "" && !isClusterNotification(r) {
			wg.Add(1)
			go func(address string, instances []string) {
				defer wg.Done()
				cert := d.endpoints.NetworkCert()

				if recursion == 1 {
					cs, err := doInstancesGetFromNode(project, address, instanceType, cert)
					if err != nil {
						for _, name := range instances {
							resultListAppend(name, api.Instance{}, err)
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
					for _, name := range instances {
						resultFullListAppend(name, api.ContainerFull{}, err)
					}

					return
				}

				for _, c := range cs {
					resultFullListAppend(c.Name, c, nil)
				}
			}(address, instances)

			continue
		}

		if recursion == 0 {
			for _, instanceName := range instances {
				path := "instances"
				// Use the container path in the generated URL if container list
				// endpoint used to avoid breaking old code that expects this.
				if instanceType == instance.TypeContainer {
					path = "containers"
				}
				url := fmt.Sprintf("/%s/%s/%s", version.APIVersion, path, instanceName)
				resultString = append(resultString, url)
			}
		} else {
			threads := 4
			if len(instances) < threads {
				threads = len(instances)
			}

			queue := make(chan string, threads)

			for i := 0; i < threads; i++ {
				wg.Add(1)

				go func() {
					for {
						instanceName, more := <-queue
						if !more {
							break
						}

						if recursion == 1 {
							c, _, err := nodeCts[instanceName].Render()
							if err != nil {
								resultListAppend(instanceName, api.Instance{}, err)
							} else {
								resultListAppend(instanceName, api.Instance(*c.(*api.Container)), err)
							}

							continue
						}

						c, _, err := nodeCts[instanceName].RenderFull()
						if err != nil {
							resultFullListAppend(instanceName, api.ContainerFull{}, err)
						} else {
							resultFullListAppend(instanceName, *c, err)
						}
					}

					wg.Done()
				}()
			}

			for _, instanceName := range instances {
				queue <- instanceName
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

// doInstancesGetFromNode Fetch information about the instances on the given remote node, using the
// rest API and with a timeout of 30 seconds.
func doInstancesGetFromNode(project, node string, instanceType instance.Type, cert *shared.CertInfo) ([]api.Instance, error) {
	f := func() ([]api.Instance, error) {
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to node %s", node)
		}

		client = client.UseProject(project)

		var instances []api.Instance

		if instanceType == instance.TypeAny {
			instances, err = client.GetInstances()
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to get instances from node %s", node)
			}
		} else if instanceType == instance.TypeContainer {
			containers, err := client.GetContainers()
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to get containers from node %s", node)

			}
			instances = make([]api.Instance, len(containers))
			for k, v := range containers {
				instances[k] = api.Instance(v)
			}
		} else {
			return nil, errors.Wrapf(err, "Failed to get instances from node %s: Unknown instance type", node)
		}

		return instances, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var instances []api.Instance
	var err error

	go func() {
		instances, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("Timeout getting instances from node %s", node)
	case <-done:
	}

	return instances, err
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
