package main

import (
	"fmt"
	"os"
	"reflect"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd-export/core"
	"github.com/canonical/lxd/lxd-export/core/export"
	importer "github.com/canonical/lxd/lxd-export/core/import"
	"github.com/canonical/lxd/lxd-export/core/logger"
	"gonum.org/v1/gonum/graph/simple"
)

func lxdClients() (sourceClient lxd.InstanceServer, targetClient lxd.InstanceServer, err error) {
	certPEMBlock, err := os.ReadFile("client.crt")
	if err != nil {
		return nil, nil, err
	}

	keyPEMBlock, err := os.ReadFile("client.key")
	if err != nil {
		return nil, nil, err
	}

	sourceClient, err = lxd.ConnectLXD("https://10.237.134.184:8443", &lxd.ConnectionArgs{
		TLSClientCert:      string(certPEMBlock),
		TLSClientKey:       string(keyPEMBlock),
		InsecureSkipVerify: true,
		SkipGetServer:      true,
	})
	if err != nil {
		return nil, nil, err
	}

	// ssh -L 127.0.0.1:8444:10.237.134.244:8443 infinity@192.168.1.29
	//
	// I have a LXD cluster whose one of the node is insidde a VM (`micro1` with endpoint at `10.237.134.244:8443`)
	// on my remote laptop (infinity@192.168.1.29)
	targetClient, err = lxd.ConnectLXD("https://10.237.134.180:8443", &lxd.ConnectionArgs{
		TLSClientCert:      string(certPEMBlock),
		TLSClientKey:       string(keyPEMBlock),
		InsecureSkipVerify: true,
		SkipGetServer:      true,
	})
	if err != nil {
		return nil, nil, err
	}

	return sourceClient, targetClient, nil
}

// compareGraphs compares two directed graphs and returns a boolean indicating
// whether they are equal and a slice of differences if they are not.
func compareGraphs(g1, g2 *simple.DirectedGraph) (bool, []string) {
	diffs := []string{}

	nodeIDs1 := make(map[int64]struct{})
	nodes1 := g1.Nodes()
	for nodes1.Next() {
		nodeIDs1[nodes1.Node().ID()] = struct{}{}
	}

	nodeIDs2 := make(map[int64]struct{})
	nodes2 := g2.Nodes()
	for nodes2.Next() {
		nodeIDs2[nodes2.Node().ID()] = struct{}{}
	}

	for id := range nodeIDs1 {
		_, exists := nodeIDs2[id]
		if !exists {
			diffs = append(diffs, fmt.Sprintf("Node %d is in g1 but not in g2", id))
		}
	}

	// Nodes in g2 but not in g1.
	for id := range nodeIDs2 {
		if _, exists := nodeIDs1[id]; !exists {
			diffs = append(diffs, fmt.Sprintf("Node %d is in g2 but not in g1", id))
		}
	}

	// Build sets of edges for g1 and g2.
	edgeSet1 := make(map[[2]int64]struct{})
	edges1 := g1.Edges()
	for edges1.Next() {
		e := edges1.Edge()
		fromID := e.From().ID()
		toID := e.To().ID()
		edgeSet1[[2]int64{fromID, toID}] = struct{}{}
	}

	edgeSet2 := make(map[[2]int64]struct{})
	edges2 := g2.Edges()
	for edges2.Next() {
		e := edges2.Edge()
		fromID := e.From().ID()
		toID := e.To().ID()
		edgeSet2[[2]int64{fromID, toID}] = struct{}{}
	}

	// Edges in g1 but not in g2.
	for edge := range edgeSet1 {
		_, exists := edgeSet2[edge]
		if !exists {
			diffs = append(diffs, fmt.Sprintf("Edge from %d to %d is in g1 but not in g2", edge[0], edge[1]))
		}
	}

	// Edges in g2 but not in g1.
	for edge := range edgeSet2 {
		_, exists := edgeSet1[edge]
		if !exists {
			diffs = append(diffs, fmt.Sprintf("Edge from %d to %d is in g2 but not in g1", edge[0], edge[1]))
		}
	}

	return len(diffs) == 0, diffs
}

func run() error {
	// Get the source and target cluster clients
	sourceClient, targetClient, err := lxdClients()
	if err != nil {
		return err
	}

	logger, err := logger.NewSafeLogger("test.log")
	if err != nil {
		return err
	}

	// Generate a DAG of the source cluster
	sourceDAG, sourceHumanToGraphID, err := export.ExportClusterDAG(sourceClient, logger)
	if err != nil {
		return err
	}

	rootID, ok := sourceHumanToGraphID["root"]
	if !ok {
		return fmt.Errorf("root node not found in the source cluster")
	}

	// Serialize the DAG
	err = core.MarshalJSON(sourceDAG, rootID, "example_exported_cluster.json")
	if err != nil {
		return err
	}

	// Now, import back the file and check that it is equal to 'sourceDAG' (it should be equal)
	importedDAG, importedHumanIDtoGraphID, err := importer.ImportClusterDAG("example_exported_cluster.json")
	if err != nil {
		return err
	}

	// Check that the humanID mappings are the same
	if !reflect.DeepEqual(sourceHumanToGraphID, importedHumanIDtoGraphID) {
		fmt.Println("sourceHumanToGraphID", sourceHumanToGraphID)
		fmt.Println("importedHumanIDtoGraphID", importedHumanIDtoGraphID)
		return fmt.Errorf("imported DAG has different number of nodes than the source DAG")
	}

	// Check that the graphs are the same
	equal, diffs := compareGraphs(sourceDAG, importedDAG)
	if !equal {
		return fmt.Errorf("imported DAG is different from the source DAG: %v", diffs)
	}

	// IMPORT on target
	targetDAG, targetHumanToGraphID, err := export.ExportClusterDAG(targetClient, logger)
	if err != nil {
		return err
	}

	importPlan, err := importer.PlanImport(sourceDAG, sourceHumanToGraphID, targetDAG, targetHumanToGraphID, targetClient)
	if err != nil {
		return err
	}

	fmt.Println(importPlan.String())

	return nil
}

func main() {
	err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
