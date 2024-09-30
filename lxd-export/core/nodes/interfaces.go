package nodes

import (
	"github.com/r3labs/diff/v3"
	"gonum.org/v1/gonum/graph"
)

// Improvement idea: we could add DOT encoding methods to implement the behavior at https://github.com/gonum/gonum/blob/master/graph/encoding/dot/encode.go
// to have a visual representation of the graph (e.g, for debugging purposes).

type Node interface {
	graph.Node

	// HumanID is useful for YAML serialization purposes.
	// Else, the ID() method is used for internal graph representation and is meaningless to the user.
	HumanID() string
	// Diff is used to compare two nodes and return the differences between them.
	Diff(other any) (diff.Changelog, error)
	Data() any

	// Renamable is used to specify if a node can be renamed.
	// TODO: The implementations of this interface (that are representing a LXD entity) should
	// have some kind of 'volatile.uuid' config field that is used to uniquely identify the entity
	// (this is currently only done for api.Instance)
	Renamable() bool
}
