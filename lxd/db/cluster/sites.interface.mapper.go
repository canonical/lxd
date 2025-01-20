//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// SiteGenerated is an interface of generated methods for Site.
type SiteGenerated interface {
	// GetSiteConfig returns all available Site Config
	// generator: site GetMany
	GetSiteConfig(ctx context.Context, tx *sql.Tx, siteID int, filters ...ConfigFilter) (map[string]string, error)

	// GetSites returns all available sites.
	// generator: site GetMany
	GetSites(ctx context.Context, tx *sql.Tx, filters ...SiteFilter) ([]Site, error)

	// GetSite returns the site with the given key.
	// generator: site GetOne
	GetSite(ctx context.Context, tx *sql.Tx, name string) (*Site, error)

	// GetSiteID return the ID of the site with the given key.
	// generator: site ID
	GetSiteID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// SiteExists checks if a site with the given key exists.
	// generator: site Exists
	SiteExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// CreateSiteConfig adds new site Config to the database.
	// generator: site Create
	CreateSiteConfig(ctx context.Context, tx *sql.Tx, siteID int64, config map[string]string) error

	// CreateSite adds a new site to the database.
	// generator: site Create
	CreateSite(ctx context.Context, tx *sql.Tx, object Site) (int64, error)

	// DeleteSite deletes the site matching the given key parameters.
	// generator: site DeleteOne-by-Name
	DeleteSite(ctx context.Context, tx *sql.Tx, name string) error

	// UpdateSiteConfig updates the site Config matching the given key parameters.
	// generator: site Update
	UpdateSiteConfig(ctx context.Context, tx *sql.Tx, siteID int64, config map[string]string) error

	// UpdateSite updates the site matching the given key parameters.
	// generator: site Update
	UpdateSite(ctx context.Context, tx *sql.Tx, name string, object Site) error

	// RenameSite renames the site matching the given key parameters.
	// generator: site Rename
	RenameSite(ctx context.Context, tx *sql.Tx, name string, to string) error
}
