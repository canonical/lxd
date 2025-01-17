//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ClusterLinkGenerated is an interface of generated methods for ClusterLink.
type ClusterLinkGenerated interface {
	// GetClusterLinks returns all available cluster_links.
	// generator: cluster_link GetMany
	GetClusterLinks(ctx context.Context, tx *sql.Tx, filters ...ClusterLinkFilter) ([]ClusterLink, error)

	// GetClusterLink returns the cluster_link with the given key.
	// generator: cluster_link GetOne
	GetClusterLink(ctx context.Context, tx *sql.Tx, name string) (*ClusterLink, error)

	// GetClusterLinkID return the ID of the cluster_link with the given key.
	// generator: cluster_link ID
	GetClusterLinkID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// ClusterLinkExists checks if a cluster_link with the given key exists.
	// generator: cluster_link Exists
	ClusterLinkExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// CreateClusterLink adds a new cluster_link to the database.
	// generator: cluster_link Create
	CreateClusterLink(ctx context.Context, tx *sql.Tx, object ClusterLink) (int64, error)

	// UpdateClusterLink updates the cluster_link matching the given key parameters.
	// generator: cluster_link Update
	UpdateClusterLink(ctx context.Context, tx *sql.Tx, name string, object ClusterLink) error

	// DeleteClusterLink deletes the cluster_link matching the given key parameters.
	// generator: cluster_link DeleteOne-by-Name
	DeleteClusterLink(ctx context.Context, tx *sql.Tx, name string) error

	// RenameClusterLink renames the cluster_link matching the given key parameters.
	// generator: cluster_link Rename
	RenameClusterLink(ctx context.Context, tx *sql.Tx, name string, to string) error
}
