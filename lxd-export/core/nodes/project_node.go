package nodes

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
	"github.com/r3labs/diff/v3"
)

var ProjectPrefix = "project_"

type ProjectNode struct {
	baseNode

	Name string
}

func (p *ProjectNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func (pn *ProjectNode) Renamable() bool {
	return true
}

func GenerateProjectHumanID(name string) string {
	return fmt.Sprintf("%s%s", ProjectPrefix, name)
}

func NewProjectNode(name string, data api.Project, id int64) *ProjectNode {
	// We don't need this field to represent the inner data in the graph.
	// The 'used by' relationships are already represented and exploitable in the graph topology.
	data.UsedBy = nil
	return &ProjectNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateProjectHumanID(name),
		},
		Name: name,
	}
}
