package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// ImageRegistryRow represents a single row of the image_registries table.
// db:model image_registries
type ImageRegistryRow struct {
	ID          int64                 `db:"id"`
	Name        string                `db:"name"`
	Description string                `db:"description"`
	Protocol    ImageRegistryProtocol `db:"protocol"`
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
// When reading from the database, int64 values are converted back to their constant type.pick
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
func (r *ImageRegistryRow) ToAPI(ctx context.Context, tx *sql.Tx) (*api.ImageRegistry, error) {
	config, err := GetImageRegistryConfig(ctx, tx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("Failed loading config for image registry %q: %w", r.Name, err)
	}

	return &api.ImageRegistry{
		Name:        r.Name,
		Description: r.Description,
		Protocol:    string(r.Protocol),
		Builtin:     r.Builtin,
		Config:      config,
	}, nil
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

// UpdateImageRegistry updates the existing image registry matching the given name.
func UpdateImageRegistry(ctx context.Context, tx *sql.Tx, name string, object ImageRegistryRow) error {
	return query.UpdateOne(ctx, tx, object, "WHERE name = ?", name)
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

// GetImageRegistryConfig returns associated config of the existing image registry with the given ID.
func GetImageRegistryConfig(ctx context.Context, tx *sql.Tx, registryID int64) (map[string]string, error) {
	stmt := "SELECT key, value FROM image_registries_config WHERE image_registry_id = ?"

	config := map[string]string{}
	err := query.Scan(ctx, tx, stmt, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, alreadySet := config[key]
		if alreadySet {
			return fmt.Errorf("Duplicate config row found for key %q for image registry ID %d", key, registryID)
		}

		config[key] = value

		return nil
	}, registryID)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// CreateImageRegistryConfig creates config for a new image registry with the given name.
func CreateImageRegistryConfig(ctx context.Context, tx *sql.Tx, registryID int64, config map[string]string) error {
	stmt, err := tx.Prepare("INSERT INTO image_registries_config (image_registry_id, key, value) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.ExecContext(ctx, registryID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
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
