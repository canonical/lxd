package importer

import (
	"os"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd-export/core"
	"gonum.org/v1/gonum/graph/simple"
)

func ImportClusterDAG(filename string) (dag *simple.DirectedGraph, humanIDtoGraphID map[string]int64, err error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, err
	}

	return core.UnmarshalJSON(content)
}

func PlanImport(srcDAG *simple.DirectedGraph, srcHIDtoID map[string]int64, dstDAG *simple.DirectedGraph, dstHIDtoID map[string]int64, client lxd.InstanceServer) (plan *Plan, err error) {
	planner := NewPlanner(srcDAG, srcHIDtoID, dstDAG, dstHIDtoID, client)
	plan, err = planner.GeneratePlan()
	if err != nil {
		return nil, err
	}

	return plan, nil
}

func ApplyImport(plan *Plan) error {
	return plan.Apply()
}
