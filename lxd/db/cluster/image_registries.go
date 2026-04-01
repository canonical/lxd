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

// ImageRegistriesRow represents a single row of the image_registries table.
// db:model image_registries
type ImageRegistriesRow struct {
	ID          int64                 `db:"id"`
	Name        string                `db:"name"`
	Description string                `db:"description"`
	Protocol    ImageRegistryProtocol `db:"protocol"`
	Public      bool                  `db:"public"`
	Builtin     bool                  `db:"builtin"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (ImageRegistriesRow) APIName() string {
	return "Image registry"
}

// APIPluralName implements [query.APIPluralName] for [ImageRegistriesRow] to override default pluralisation behaviour for
// error messages (which simply appends an "s").
func (ImageRegistriesRow) APIPluralName() string {
	return "Image registries"
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

// ToAPI converts the database [ImageRegistriesRow] struct to API type [api.ImageRegistry].
func (r *ImageRegistriesRow) ToAPI(allConfigs map[int64]map[string]string) *api.ImageRegistry {
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

// ImageRegistriesConfigStore returns a [query.EntityConfigStore] for image registries.
func ImageRegistriesConfigStore() *query.EntityConfigStore {
	return &query.EntityConfigStore{
		EntityTable:               "image_registries",
		ConfigTable:               "image_registries_config",
		ConfigTableEntityIDColumn: "image_registry_id",
	}
}

// GetImageRegistries returns all available image registries.
func GetImageRegistries(ctx context.Context, tx *sql.Tx) ([]ImageRegistriesRow, error) {
	return query.Select[ImageRegistriesRow](ctx, tx, "ORDER BY name")
}

// GetImageRegistry returns the image registry with the given name.
func GetImageRegistry(ctx context.Context, tx *sql.Tx, name string) (*ImageRegistriesRow, error) {
	registry, err := query.SelectOne[ImageRegistriesRow](ctx, tx, "WHERE name = ?", name)
	if err != nil {
		return nil, fmt.Errorf("Failed loading image registry: %w", err)
	}

	return registry, nil
}

// CreateImageRegistry adds a new image registry to the database.
func CreateImageRegistry(ctx context.Context, tx *sql.Tx, object ImageRegistriesRow) (int64, error) {
	return query.Create(ctx, tx, object)
}

// UpdateImageRegistry updates the existing image registry by its ID.
func UpdateImageRegistry(ctx context.Context, tx *sql.Tx, object ImageRegistriesRow) error {
	return query.UpdateByPrimaryKey(ctx, tx, object)
}

// DeleteImageRegistry deletes the image registry with the given name from the database.
func DeleteImageRegistry(ctx context.Context, tx *sql.Tx, name string) error {
	return query.DeleteOne[ImageRegistriesRow, *ImageRegistriesRow](ctx, tx, "WHERE name = ?", name)
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
	store := ImageRegistriesConfigStore()

	if registryID != nil {
		return store.GetByEntityIDs(ctx, tx, *registryID)
	}

	return store.GetAll(ctx, tx)
}

// CreateImageRegistryConfig creates config for a new image registry with the given name.
func CreateImageRegistryConfig(ctx context.Context, tx *sql.Tx, registryID int64, config map[string]string) error {
	return ImageRegistriesConfigStore().Set(ctx, tx, registryID, config)
}

// GetImageRegistriesAndURLs returns all image registries that pass the given filter, along with their entity URLs.
func GetImageRegistriesAndURLs(ctx context.Context, tx *sql.Tx, filter func(registry ImageRegistriesRow) bool) ([]ImageRegistriesRow, []string, error) {
	var imageRegistries []ImageRegistriesRow
	var imageRegistryURLs []string

	err := query.SelectFunc[ImageRegistriesRow](ctx, tx, "ORDER BY name", func(registry ImageRegistriesRow) error {
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
