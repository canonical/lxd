package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ImageRegistryRow represents a single row of the image_registries table.
// db:model image_registries
type ImageRegistryRow struct {
	ID          int64                 `db:"id"`
	Name        string                `db:"name"`
	Description string                `db:"description"`
	Protocol    ImageRegistryProtocol `db:"protocol"`
	Public      bool                  `db:"public"`
	Builtin     bool                  `db:"builtin"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (ImageRegistryRow) APIName() string {
	return "Image registry"
}

// ImageRegistryProtocol represents the types of supported image registry protocols.
//
// This type implements the [sql.Scanner] and [driver.Value] interfaces to automatically handle
// conversion between API constants and their int64 representation in the database.
// When reading from the database, int64 values are converted back to their constant type.
// When writing to the database, API constants are converted to their int64 representation.
type ImageRegistryProtocol string

const (
	protocolSimpleStreams int64 = iota // Image registry protocol "SimpleStreams".
	protocolLXD                        // Image registry protocol "LXD".
)

// ScanInteger implements [query.IntegerScanner] for [ImageRegistryProtocol].
func (p *ImageRegistryProtocol) ScanInteger(protocolCode int64) error {
	switch protocolCode {
	case protocolSimpleStreams:
		*p = api.ImageRegistryProtocolSimpleStreams
	case protocolLXD:
		*p = api.ImageRegistryProtocolLXD
	default:
		return fmt.Errorf("Unknown image registry protocol `%d`", protocolCode)
	}

	return nil
}

// Scan implements [sql.Scanner] for [ImageRegistryProtocol]. This converts the database integer value back into the correct API constant or returns an error.
func (p *ImageRegistryProtocol) Scan(value any) error {
	return query.ScanValue(value, p, false)
}

// Value implements [driver.Value] for [ImageRegistryProtocol]. This converts the API constant into its integer database representation or throws an error.
func (p ImageRegistryProtocol) Value() (driver.Value, error) {
	switch p {
	case api.ImageRegistryProtocolSimpleStreams:
		return protocolSimpleStreams, nil
	case api.ImageRegistryProtocolLXD:
		return protocolLXD, nil
	}

	return nil, fmt.Errorf("Invalid image registry protocol %q", p)
}

// ToAPI converts the database [ImageRegistryRow] struct to API type [api.ImageRegistry].
func (r *ImageRegistryRow) ToAPI(allConfigs map[int64]map[string]string) *api.ImageRegistry {
	config := allConfigs[r.ID]
	if config == nil {
		config = map[string]string{}
	}

	return &api.ImageRegistry{
		Name:        r.Name,
		Description: r.Description,
		Protocol:    string(r.Protocol),
		Public:      r.Public,
		Builtin:     r.Builtin,
		Config:      config,
	}
}

// GetImageRegistries returns all available image registries.
func GetImageRegistries(ctx context.Context, tx *sql.Tx) ([]ImageRegistryRow, error) {
	return query.Select[ImageRegistryRow](ctx, tx, "ORDER BY name")
}

// GetImageRegistry returns the image registry with the given name.
func GetImageRegistry(ctx context.Context, tx *sql.Tx, name string) (*ImageRegistryRow, error) {
	registry, err := query.SelectOne[ImageRegistryRow](ctx, tx, "WHERE name = ?", name)
	if err != nil {
		return nil, fmt.Errorf("Failed loading image registry: %w", err)
	}

	return registry, nil
}

// CreateImageRegistry adds a new image registry to the database.
func CreateImageRegistry(ctx context.Context, tx *sql.Tx, object ImageRegistryRow) (int64, error) {
	return query.Create(ctx, tx, object)
}

// UpdateImageRegistry updates the existing image registry by its ID.
func UpdateImageRegistry(ctx context.Context, tx *sql.Tx, object ImageRegistryRow) error {
	return query.UpdateByPrimaryKey(ctx, tx, object)
}

// DeleteImageRegistry deletes the image registry with the given name from the database.
func DeleteImageRegistry(ctx context.Context, tx *sql.Tx, name string) error {
	return query.DeleteOne[ImageRegistryRow, *ImageRegistryRow](ctx, tx, "WHERE name = ?", name)
}

// RenameImageRegistry renames an existing image registry with the given name.
func RenameImageRegistry(ctx context.Context, tx *sql.Tx, name string, to string) error {
	registry, err := GetImageRegistry(ctx, tx, name)
	if err != nil {
		return err
	}

	registry.Name = to
	return query.UpdateByPrimaryKey(ctx, tx, *registry)
}

// GetImageRegistryConfig returns the config for all image registries, or only the config for the image registry with the given ID if provided.
func GetImageRegistryConfig(ctx context.Context, tx *sql.Tx, registryID *int64) (map[int64]map[string]string, error) {
	var q string
	var args []any

	if registryID != nil {
		q = `SELECT image_registry_id, key, value FROM image_registries_config WHERE image_registry_id=?`
		args = []any{*registryID}
	} else {
		q = `SELECT image_registry_id, key, value FROM image_registries_config`
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
			return fmt.Errorf("Duplicate config row found for key %q for image registry ID %d", key, id)
		}

		allConfigs[id][key] = value

		return nil
	}, args...)
}

// CreateImageRegistryConfig creates config for a new image registry with the given name.
func CreateImageRegistryConfig(ctx context.Context, tx *sql.Tx, registryID int64, config map[string]string) error {
	return createEntityConfig(ctx, tx, "image_registries_config", "image_registry_id", registryID, config)
}

// UpdateImageRegistryConfig updates the existing image registry with the given name by setting its config.
func UpdateImageRegistryConfig(ctx context.Context, tx *sql.Tx, registryID int64, config map[string]string) error {
	// Clear the config.
	_, err := tx.ExecContext(ctx, "DELETE FROM image_registries_config WHERE image_registry_id = ?", registryID)
	if err != nil {
		return err
	}

	return CreateImageRegistryConfig(ctx, tx, registryID, config)
}

// GetImageRegistriesAndURLs returns all image registries that pass the given filter, along with their entity URLs.
func GetImageRegistriesAndURLs(ctx context.Context, tx *sql.Tx, filter func(registry ImageRegistryRow) bool) ([]ImageRegistryRow, []string, error) {
	var imageRegistries []ImageRegistryRow
	var imageRegistryURLs []string

	err := query.SelectFunc[ImageRegistryRow](ctx, tx, "ORDER BY name", func(registry ImageRegistryRow) error {
		if filter != nil && !filter(registry) {
			return nil
		}

		imageRegistries = append(imageRegistries, registry)
		imageRegistryURLs = append(imageRegistryURLs, entity.ImageRegistryURL(registry.Name).String())
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return imageRegistries, imageRegistryURLs, nil
}
