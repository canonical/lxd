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
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/filter"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// urlInstanceTypeDetect detects what sort of instance type filter is being requested. Either
// explicitly via the instance-type query param or implicitly via the endpoint URL used.
func urlInstanceTypeDetect(r *http.Request) (instancetype.Type, error) {
	reqInstanceType := r.URL.Query().Get("instance-type")
	if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
		return instancetype.Container, nil
	} else if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "vm") {
		return instancetype.VM, nil
	} else if reqInstanceType != "" {
		instanceType, err := instancetype.New(reqInstanceType)
		if err != nil {
			return instancetype.Any, err
		}
		return instanceType, nil
	}

	return instancetype.Any, nil
}

// swagger:operation GET /1.0/instances instances instances_get
//
// Get the instances
//
// Returns a list of instances (URLs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: filter
//     description: Collection filter
//     type: string
//     example: default
//   - in: query
//     name: all-projects
//     description: Retrieve instances from all projects
//     type: boolean
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/instances/foo",
//               "/1.0/instances/bar"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances?recursion=1 instances instances_get_recursion1
//
// Get the instances
//
// Returns a list of instances (basic structs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: filter
//     description: Collection filter
//     type: string
//     example: default
//   - in: query
//     name: all-projects
//     description: Retrieve instances from all projects
//     type: boolean
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of instances
//           items:
//             $ref: "#/definitions/Instance"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances?recursion=2 instances instances_get_recursion2
//
// Get the instances
//
// Returns a list of instances (full structs).
//
// The main difference between recursion=1 and recursion=2 is that the
// latter also includes state and snapshot information allowing for a
// single API call to return everything needed by most clients.
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: filter
//     description: Collection filter
//     type: string
//     example: default
//   - in: query
//     name: all-projects
//     description: Retrieve instances from all projects
//     type: boolean
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of instances
//           items:
//             $ref: "#/definitions/InstanceFull"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

func instancesGet(d *Daemon, r *http.Request) response.Response {
	for i := 0; i < 100; i++ {
		result, err := doInstancesGet(d, r)
		if err == nil {
			return response.SyncResponse(true, result)
		}
		if !query.IsRetriableError(err) {
			logger.Debugf("DBERR: containersGet: error %q", err)
			return response.SmartError(err)
		}
		// 100 ms may seem drastic, but we really don't want to thrash
		// perhaps we should use a random amount
		time.Sleep(100 * time.Millisecond)
	}

	logger.Debugf("DBERR: containersGet, db is locked")
	logger.Debugf(logger.GetStack())
	return response.InternalError(fmt.Errorf("DB is locked"))
}

func doInstancesGet(d *Daemon, r *http.Request) (interface{}, error) {
	resultString := []string{}
	resultList := []*api.Instance{}
	resultFullList := []*api.InstanceFull{}
	resultMu := sync.Mutex{}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return nil, err
	}

	// Parse the recursion field
	recursionStr := r.FormValue("recursion")

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	// Parse filter value
	filterStr := r.FormValue("filter")
	var clauses []filter.Clause
	if filterStr != "" {
		clauses, err = filter.Parse(filterStr)
		if err != nil {
			return nil, errors.Wrap(err, "Invalid filter")
		}
	}

	// Parse the project field
	projectName := projectParam(r)

	// Parse all-projects field
	allProjects := r.FormValue("all-projects")

	// Get the list and location of all containers
	var result map[string][]string // Containers by node address
	var nodes map[string]string    // Node names by container
	filteredProjects := []string{}
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		if allProjects == "true" {
			projects, err := tx.GetProjects(db.ProjectFilter{})
			if err != nil {
				return err
			}

			for _, project := range projects {
				if !rbac.UserHasPermission(r, project.Name, "view") {
					continue
				}

				filteredProjects = append(filteredProjects, project.Name)
			}
		} else {
			filteredProjects = []string{projectName}
		}

		result, err = tx.GetInstanceNamesByNodeAddress(filteredProjects, db.InstanceTypeFilter(instanceType))
		if err != nil {
			return err
		}

		nodes, err = tx.GetInstanceToNodeMap(filteredProjects, db.InstanceTypeFilter(instanceType))
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return []string{}, err
	}

	// Get the local instances
	nodeInstances := map[string]instance.Instance{}
	mustLoadObjects := recursion > 0 || (recursion == 0 && clauses != nil)
	if mustLoadObjects {
		for _, project := range filteredProjects {
			insts, err := instanceLoadNodeProjectAll(d.State(), project, instanceType)
			if err != nil {
				return nil, err
			}

			for _, inst := range insts {
				nodeInstances[inst.Name()] = inst
			}
		}
	}

	// Append containers to list and handle errors
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

	resultFullListAppend := func(name string, c api.InstanceFull, err error) {
		if err != nil {
			c = api.InstanceFull{Instance: api.Instance{
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
	networkCert := d.endpoints.NetworkCert()
	for address, instanceNames := range result {
		// If this is an internal request from another cluster node,
		// ignore containers from other nodes, and return only the ones
		// on this node
		if isClusterNotification(r) && address != "" {
			continue
		}

		// Mark containers on unavailable nodes as down
		if mustLoadObjects && address == "0.0.0.0" {
			for _, instanceName := range instanceNames {
				if recursion < 2 {
					resultListAppend(instanceName, api.Instance{}, fmt.Errorf("unavailable"))
				} else {
					resultFullListAppend(instanceName, api.InstanceFull{}, fmt.Errorf("unavailable"))
				}
			}

			continue
		}

		// For recursion requests we need to fetch the state of remote
		// containers from their respective nodes.
		if mustLoadObjects && address != "" && !isClusterNotification(r) {
			wg.Add(1)
			go func(address string, containers []string) {
				defer wg.Done()

				if recursion == 1 {
					cs, err := doContainersGetFromNode(filteredProjects, address, allProjects, networkCert, d.serverCert(), r, instanceType)
					if err != nil {
						for _, name := range containers {
							resultListAppend(name, api.Instance{}, err)
						}

						return
					}

					for _, c := range cs {
						resultListAppend(c.Name, c, nil)
					}

					return
				}

				cs, err := doContainersFullGetFromNode(filteredProjects, address, allProjects, networkCert, d.serverCert(), r, instanceType)
				if err != nil {
					for _, name := range containers {
						resultFullListAppend(name, api.InstanceFull{}, err)
					}

					return
				}

				for _, c := range cs {
					resultFullListAppend(c.Name, c, nil)
				}
			}(address, instanceNames)

			continue
		}
		if !mustLoadObjects {
			for _, instanceName := range instanceNames {
				instancePath := "instances"
				if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
					instancePath = "containers"
				} else if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "vm") {
					instancePath = "virtual-machines"
				}
				url := fmt.Sprintf("/%s/%s/%s", version.APIVersion, instancePath, instanceName)
				resultString = append(resultString, url)
			}
		} else {
			threads := 4
			if len(instanceNames) < threads {
				threads = len(instanceNames)
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

						inst, found := nodeInstances[instanceName]
						if !found {
							continue
						}

						if recursion < 2 {
							c, _, err := inst.Render()
							if err != nil {
								resultListAppend(instanceName, api.Instance{}, err)
							} else {
								resultListAppend(instanceName, *c.(*api.Instance), err)
							}

							continue
						}

						c, _, err := inst.RenderFull()
						if err != nil {
							resultFullListAppend(instanceName, api.InstanceFull{}, err)
						} else {
							resultFullListAppend(instanceName, *c, err)
						}
					}

					wg.Done()
				}()
			}

			for _, instanceName := range instanceNames {
				queue <- instanceName
			}

			close(queue)
		}
	}
	wg.Wait()

	if recursion == 0 {
		if clauses != nil {
			for _, container := range instance.Filter(resultList, clauses) {
				instancePath := "instances"
				if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
					instancePath = "containers"
				} else if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "vm") {
					instancePath = "virtual-machines"
				}
				url := fmt.Sprintf("/%s/%s/%s", version.APIVersion, instancePath, container.Name)
				resultString = append(resultString, url)
			}
		}
		return resultString, nil
	}

	if recursion == 1 {
		// Sort the result list by name.
		sort.Slice(resultList, func(i, j int) bool {
			return resultList[i].Name < resultList[j].Name
		})
		if clauses != nil {
			resultList = instance.Filter(resultList, clauses)
		}
		return resultList, nil
	}

	// Sort the result list by name.
	sort.Slice(resultFullList, func(i, j int) bool {
		return resultFullList[i].Name < resultFullList[j].Name
	})

	if clauses != nil {
		resultFullList = instance.FilterFull(resultFullList, clauses)
	}
	return resultFullList, nil
}

// Fetch information about the containers on the given remote node, using the
// rest API and with a timeout of 30 seconds.
func doContainersGetFromNode(projects []string, node, allProjects string, networkCert *shared.CertInfo, serverCert *shared.CertInfo, r *http.Request, instanceType instancetype.Type) ([]api.Instance, error) {
	f := func() ([]api.Instance, error) {
		client, err := cluster.Connect(node, networkCert, serverCert, r, true)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to node %s", node)
		}

		var containers []api.Instance
		if allProjects == "true" {
			containers, err = client.GetInstancesAllProjects(api.InstanceType(instanceType.String()))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to get instances from node %s", node)
			}

		} else {
			for _, project := range projects {
				client = client.UseProject(project)

				tmpContainers, err := client.GetInstances(api.InstanceType(instanceType.String()))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to get instances from node %s", node)
				}
				containers = append(containers, tmpContainers...)
			}

		}

		return containers, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var containers []api.Instance
	var err error

	go func() {
		containers, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("Timeout getting instances from node %s", node)
	case <-done:
	}

	return containers, err
}

func doContainersFullGetFromNode(projects []string, node, allProjects string, networkCert *shared.CertInfo, serverCert *shared.CertInfo, r *http.Request, instanceType instancetype.Type) ([]api.InstanceFull, error) {
	f := func() ([]api.InstanceFull, error) {
		client, err := cluster.Connect(node, networkCert, serverCert, r, true)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to node %s", node)
		}

		var instances []api.InstanceFull
		if allProjects == "true" {
			instances, err = client.GetInstancesFullAllProjects(api.InstanceType(instanceType.String()))
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to get instances from node %s", node)
			}
		} else {
			for _, project := range projects {
				client = client.UseProject(project)

				tmpInstances, err := client.GetInstancesFull(api.InstanceType(instanceType.String()))
				if err != nil {
					return nil, errors.Wrapf(err, "Failed to get instances from node %s", node)
				}

				instances = append(instances, tmpInstances...)
			}
		}

		return instances, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var instances []api.InstanceFull
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
