package export

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd-export/core/logger"
	"github.com/canonical/lxd/lxd-export/core/nodes"
	"gonum.org/v1/gonum/graph/simple"
)

func ExportClusterDAG(client lxd.InstanceServer, logger *logger.SafeLogger) (*simple.DirectedGraph, map[string]int64, error) {
	dag := simple.NewDirectedGraph()
	humanIDtoGraphID := make(map[string]int64)

	// Add root node
	rootNode, err := nodes.NewRootNode(client, uint(0))
	if err != nil {
		return nil, nil, err
	}

	dag.AddNode(rootNode)
	humanIDtoGraphID[rootNode.HumanID()] = rootNode.ID()

	// Get default project
	projects, err := client.GetProjects()
	if err != nil {
		return nil, nil, err
	}

	if len(projects) == 0 {
		return nil, nil, errors.New("No project found")
	}

	var defaultProjectNode *nodes.ProjectNode
	otherProjectNodes := make(map[string]*nodes.ProjectNode)
	if len(projects) == 1 {
		// Only default project
		defaultProjectNode = nodes.NewProjectNode(projects[0].Name, projects[0], rootNode.Rank()+1)
		dag.AddNode(defaultProjectNode)
		humanIDtoGraphID[defaultProjectNode.HumanID()] = defaultProjectNode.ID()
		dag.SetEdge(dag.NewEdge(rootNode, defaultProjectNode))
	} else {
		for _, project := range projects {
			r := rootNode.Rank() + 1
			if project.Name != "default" {
				r += 1
			}

			projectNode := nodes.NewProjectNode(projects[0].Name, projects[0], r)
			dag.AddNode(projectNode)
			humanIDtoGraphID[projectNode.HumanID()] = projectNode.ID()
			if project.Name == "default" {
				defaultProjectNode = projectNode
				dag.SetEdge(dag.NewEdge(rootNode, projectNode))
			} else {
				otherProjectNodes[project.Name] = projectNode
			}
		}

		for _, otherProjectNode := range otherProjectNodes {
			dag.SetEdge(dag.NewEdge(defaultProjectNode, otherProjectNode))
		}
	}

	// Now, we can build the rest of the DAG concurrently.
	//
	// Each project is processed concurrently as their resources are independent from each other, except for one
	// special case which is the 'network peers' that can connect networks from two different projects). For this special case,
	// we have an internal barrier mechanism to ensure that all 'network' entities for all the project workers are processed before
	// adding the 'network peers' to the DAG.
	//
	// - In each project worker, we can even process the 'network' and 'storage' entities concurrently as they are
	//   independent from each other.
	// - Once these two are processed in one project, we can process the 'instance' entities.

	var dagWorker *DAGWorker
	if len(otherProjectNodes) == 0 {
		dagWorker = NewDAGWorker(
			client,
			logger,
			"default", // the project name is the worker ID
			nil,       // No need for a broadcaster as we have only one DAG worker in this case.
			1,
			rootNode,
			defaultProjectNode,
			nil, // No need to know the project 'features' as we have only one project.
			dag,
			&sync.RWMutex{},
			humanIDtoGraphID, // A map to store the humanID to nodeID mapping
			&sync.RWMutex{},
			defaultProjectNode.Rank(),
		)
		err = dagWorker.Start()
		if err != nil {
			return nil, nil, err
		}

		return dagWorker.graph, dagWorker.humanIDtoNodeID, nil
	}

	// If we have multiple projects, the DAG is much more complex. Some entities belonging to a project
	// might be dependent on entities from another project: network peers or other entities with a 'target-project' property.
	// But the also the simple fact that a project can inherit from the default project (or not) through the `features` property,
	// creates cross project dependencies.
	// We chose to concurrently build the DAG for each project with a synchronization mechanism to ensure that the complete DAG is built
	// with the right cross-project dependencies.

	// Create a sync hub to communicate between workers and some mutexes for shared resources
	broadcaster := NewBroadcaster()
	muGraph := sync.RWMutex{}
	muHumanIDtoNodeID := sync.RWMutex{}
	totalWorkers := uint(len(otherProjectNodes) + 1)

	// Extract the features for each project as it impacts the DAG dependencies.
	featuresPerProject := func() map[string]map[string]string {
		res := make(map[string]map[string]string)
		for _, project := range projects {
			name := project.Name
			features := make(map[string]string)
			for k, v := range project.Config {
				if strings.HasPrefix(k, "features.") {
					features[k] = v
				}
			}

			res[name] = features
		}

		return res
	}()

	// Create the 'default' project worker
	workers := make([]*DAGWorker, len(otherProjectNodes)+1)
	dagWorker = NewDAGWorker(
		client,
		logger,
		"default",
		broadcaster,
		totalWorkers,
		rootNode,
		defaultProjectNode,
		featuresPerProject["default"],
		dag,
		&muGraph,
		humanIDtoGraphID,
		&muHumanIDtoNodeID,
		defaultProjectNode.Rank(),
	)

	workers[0] = dagWorker
	i := 1
	for projectName, projectNode := range otherProjectNodes {
		workers[i] = NewDAGWorker(
			client.UseProject(projectName),
			logger,
			projectName,
			broadcaster,
			totalWorkers,
			rootNode,
			projectNode,
			featuresPerProject[projectName],
			dag,
			&muGraph,
			humanIDtoGraphID,
			&muHumanIDtoNodeID,
			projectNode.Rank(),
		)
	}

	// Start the workers
	workersErrorChan := make(chan error, len(otherProjectNodes)+1)
	var workersWG sync.WaitGroup
	for _, worker := range workers {
		workersWG.Add(1)
		go func(w *DAGWorker) {
			defer workersWG.Done()
			err := w.Start()
			if err != nil {
				workersErrorChan <- err
			}
		}(worker)
	}

	// Wait for all workers to finish
	go func() {
		workersWG.Wait()
		close(workersErrorChan)
	}()

	// Collect and return worker errors
	var workerErrors []error
	for err := range workersErrorChan {
		workerErrors = append(workerErrors, err)
	}

	// Return errors if any
	if len(workerErrors) > 0 {
		return nil, nil, fmt.Errorf("Error(s) while running the DAG workers: %v", workerErrors)
	}

	// Use the worker used for the 'default' project to get the reference for the underlying graph
	// that should contain the results of the other workers.
	return dagWorker.graph, dagWorker.humanIDtoNodeID, nil
}
