package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ClusterLinkType represents the type of a cluster link stored as a string in the database.
//
// This type implements the [sql.Scanner] and [driver.Value] interfaces to automatically handle conversion between API constants and their int64 representation in the database.
// When reading from the database, int64 values are converted back to their constant type.
// When writing to the database, API constants are converted to their int64 representation.
type ClusterLinkType string

const (
	clusterLinkTypeBidirectional  int64 = 0
	clusterLinkTypeUnidirectional int64 = 1
)

// ClusterLinkRow represents a single row of the cluster_links table.
// db:model cluster_links
type ClusterLinkRow struct {
	ID          int64           `db:"id"`
	IdentityID  *int64          `db:"identity_id"`
	Name        string          `db:"name"`
	Description string          `db:"description"`
	Type        ClusterLinkType `db:"type"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (ClusterLinkRow) APIName() string {
	return "Cluster link"
}

// ScanInteger implements [query.IntegerScanner] for [ClusterLinkType].
func (c *ClusterLinkType) ScanInteger(clusterLinkTypeCode int64) error {
	switch clusterLinkTypeCode {
	case clusterLinkTypeBidirectional:
		*c = api.ClusterLinkTypeBidirectional
	case clusterLinkTypeUnidirectional:
		*c = api.ClusterLinkTypeUnidirectional
	default:
		return fmt.Errorf("Unknown cluster link type %d", clusterLinkTypeCode)
	}

	return nil
}

// Scan implements [sql.Scanner] for [ClusterLinkType]. This converts the database integer value back into the correct API constant or returns an error.
func (c *ClusterLinkType) Scan(value any) error {
	return query.ScanValue(value, c, false)
}

// Value implements [driver.Value] for [ClusterLinkType]. This converts the API constant into its integer database representation or throws an error.
func (c ClusterLinkType) Value() (driver.Value, error) {
	switch c {
	case api.ClusterLinkTypeBidirectional:
		return clusterLinkTypeBidirectional, nil
	case api.ClusterLinkTypeUnidirectional:
		return clusterLinkTypeUnidirectional, nil
	}

	return nil, fmt.Errorf("Invalid cluster link type %q", c)
}

// ToAPI converts the database [ClusterLinkRow] struct to API type [api.ClusterLink].
func (r *ClusterLinkRow) ToAPI(allConfigs map[int64]map[string]string) *api.ClusterLink {
	config := allConfigs[r.ID]
	if config == nil {
		config = map[string]string{}
	}

	return &api.ClusterLink{
		Name:        r.Name,
		Description: r.Description,
		Type:        string(r.Type),
		Config:      config,
	}
}

// GetClusterLinks returns all cluster links.
func GetClusterLinks(ctx context.Context, tx *sql.Tx) ([]ClusterLinkRow, error) {
	return query.Select[ClusterLinkRow](ctx, tx, "ORDER BY name")
}

// GetClusterLink returns the cluster link with the given name.
func GetClusterLink(ctx context.Context, tx *sql.Tx, name string) (*ClusterLinkRow, error) {
	link, err := query.SelectOne[ClusterLinkRow](ctx, tx, "WHERE name = ?", name)
	if err != nil {
		return nil, fmt.Errorf("Failed loading cluster link: %w", err)
	}

	return link, nil
}

// CreateClusterLink adds a new cluster link to the database.
func CreateClusterLink(ctx context.Context, tx *sql.Tx, object ClusterLinkRow) (int64, error) {
	return query.Create(ctx, tx, object)
}

// UpdateClusterLink updates the cluster link row by its ID.
func UpdateClusterLink(ctx context.Context, tx *sql.Tx, object ClusterLinkRow) error {
	return query.UpdateByPrimaryKey(ctx, tx, object)
}

// DeleteClusterLink deletes the cluster link with the given name.
func DeleteClusterLink(ctx context.Context, tx *sql.Tx, name string) error {
	return query.DeleteOne[ClusterLinkRow, *ClusterLinkRow](ctx, tx, "WHERE name = ?", name)
}

// RenameClusterLink renames the cluster link with the given name.
func RenameClusterLink(ctx context.Context, tx *sql.Tx, name string, to string) error {
	link, err := GetClusterLink(ctx, tx, name)
	if err != nil {
		return err
	}

	link.Name = to
	return query.UpdateByPrimaryKey(ctx, tx, *link)
}

// GetClusterLinkConfig returns the config for all cluster links, or only the config for the cluster link with the given ID if provided.
func GetClusterLinkConfig(ctx context.Context, tx *sql.Tx, clusterLinkID *int64) (map[int64]map[string]string, error) {
	var q string
	var args []any
	if clusterLinkID != nil {
		q = `SELECT cluster_link_id, key, value FROM cluster_links_config WHERE cluster_link_id=?`
		args = []any{*clusterLinkID}
	} else {
		q = `SELECT cluster_link_id, key, value FROM cluster_links_config`
	}

	allConfigs := map[int64]map[string]string{}
	return allConfigs, query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var id int64
		var key, value string

		err := scan(&id, &key, &value)
		if err != nil {
			return err
		}

		if allConfigs[id] == nil {
			allConfigs[id] = map[string]string{}
		}

		_, found := allConfigs[id][key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for cluster link ID %d", key, id)
		}

		allConfigs[id][key] = value

		return nil
	}, args...)
}

// CreateClusterLinkConfig creates config for a new cluster link with the given ID.
func CreateClusterLinkConfig(ctx context.Context, tx *sql.Tx, clusterLinkID int64, config map[string]string) error {
	return createEntityConfig(ctx, tx, "cluster_links_config", "cluster_link_id", clusterLinkID, config)
}

// UpdateClusterLinkConfig updates the cluster link with the given ID, setting its config.
func UpdateClusterLinkConfig(ctx context.Context, tx *sql.Tx, clusterLinkID int64, config map[string]string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM cluster_links_config WHERE cluster_link_id=?", clusterLinkID)
	if err != nil {
		return err
	}

	return CreateClusterLinkConfig(ctx, tx, clusterLinkID, config)
}

// clusterConfigRef describes an entity type whose config table may reference a cluster link via the 'cluster' key.
// To support a new entity type, add an entry here.
type clusterConfigRef struct {
	typeCode    int64
	configTable string // e.g. "replicators_config"
	idColumn    string // Foreign key column in configTable, e.g. "replicator_id"
	entityTable string // e.g. "replicators"
}

// clusterConfigRefs lists every entity type whose config may contain a 'cluster' key referencing a cluster link.
var clusterConfigRefs = []clusterConfigRef{
	{
		typeCode:    entityTypeCodeReplicator,
		configTable: "replicators_config",
		idColumn:    "replicator_id",
		entityTable: "replicators",
	},
}

// GetClusterLinkUsedBy returns a list of URLs of all entities that reference the cluster link with the given name via the 'cluster' config key.
// If firstOnly is true then the search stops after the first match.
func GetClusterLinkUsedBy(ctx context.Context, tx *sql.Tx, clusterLinkName string, firstOnly bool) ([]string, error) {
	var b strings.Builder
	args := make([]any, 0, len(clusterConfigRefs))

	for i, ref := range clusterConfigRefs {
		if i > 0 {
			b.WriteString("\nUNION ")
		}

		b.WriteString(`SELECT ` + strconv.FormatInt(ref.typeCode, 10) + `, ` + ref.entityTable + `.name, projects.name FROM ` + ref.entityTable + `
JOIN ` + ref.configTable + ` ON ` + ref.entityTable + `.id = ` + ref.configTable + `.` + ref.idColumn + `
JOIN projects ON ` + ref.entityTable + `.project_id = projects.id
WHERE ` + ref.configTable + `.key = 'cluster' AND ` + ref.configTable + `.value = ?`)
		args = append(args, clusterLinkName)
	}

	if firstOnly {
		b.WriteString(" LIMIT 1")
	}

	var urls []string
	err := query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		var eType EntityType
		var eName string
		var pName string
		err := scan(&eType, &eName, &pName)
		if err != nil {
			return err
		}

		switch entity.Type(eType) {
		case entity.TypeReplicator:
			urls = append(urls, entity.ReplicatorURL(pName, eName).String())
		default:
			return errors.New("Unexpected entity type in cluster link usage query")
		}

		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed finding references to cluster link: %w", err)
	}

	return urls, nil
}

// GetClusterLinksAndURLs returns all cluster links that pass the given filter, along with their entity URLs.
func GetClusterLinksAndURLs(ctx context.Context, tx *sql.Tx, filter func(link ClusterLinkRow) bool) ([]ClusterLinkRow, []string, error) {
	var clusterLinks []ClusterLinkRow
	var clusterLinkURLs []string
	err := query.SelectFunc[ClusterLinkRow](ctx, tx, "ORDER BY name", func(link ClusterLinkRow) error {
		if filter != nil && !filter(link) {
			return nil
		}

		clusterLinks = append(clusterLinks, link)
		clusterLinkURLs = append(clusterLinkURLs, entity.ClusterLinkURL(link.Name).String())
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return clusterLinks, clusterLinkURLs, nil
}
