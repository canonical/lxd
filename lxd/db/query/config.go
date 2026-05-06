package query

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// SelectConfig executes a query statement against a "config" table, which must
// have 'key' and 'value' columns. By default this query returns all keys, but
// additional WHERE filters can be specified.
//
// Returns a map of key names to their associated values.
func SelectConfig(ctx context.Context, tx *sql.Tx, table string, where string, args ...any) (map[string]string, error) {
	query := "SELECT key, value FROM " + table
	if where != "" {
		query += " WHERE " + where
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	values := map[string]string{}
	for rows.Next() {
		var key string
		var value string

		err := rows.Scan(&key, &value)
		if err != nil {
			return nil, err
		}

		values[key] = value
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return values, nil
}

// UpdateServerConfig updates the given keys in the "config" table. Config keys set to empty values will be deleted.
func UpdateServerConfig(tx *sql.Tx, values map[string]string) error {
	changes := map[string]string{}
	deletes := []string{}

	for key, value := range values {
		if value == "" {
			deletes = append(deletes, key)
			continue
		}

		changes[key] = value
	}

	err := upsertConfig(tx, changes)
	if err != nil {
		return fmt.Errorf("updating values failed: %w", err)
	}

	err = deleteConfig(tx, deletes)
	if err != nil {
		return fmt.Errorf("deleting values failed: %w", err)
	}

	return nil
}

// Insert or updates the key/value rows of the given config table.
func upsertConfig(tx *sql.Tx, values map[string]string) error {
	if len(values) == 0 {
		return nil // Nothing to update
	}

	query := "INSERT OR REPLACE INTO config (key, value) VALUES"
	exprs := []string{}
	params := []any{}
	for key, value := range values {
		exprs = append(exprs, "(?, ?)")
		params = append(params, key, value)
	}

	query += strings.Join(exprs, ",")
	_, err := tx.Exec(query, params...)
	return err
}

// Delete the given key rows from the given config table.
func deleteConfig(tx *sql.Tx, keys []string) error {
	n := len(keys)

	if n == 0 {
		return nil // Nothing to delete.
	}

	query := "DELETE FROM config WHERE key IN " + Params(n)
	values := make([]any, n)
	for i, key := range keys {
		values[i] = key
	}

	_, err := tx.Exec(query, values...)
	return err
}

// EntityConfigStore generalises configuration management for most entities.
// It does not support entities whose configuration is node-specific (networks, storage pools).
type EntityConfigStore struct {
	EntityTable               string
	ConfigTable               string
	ConfigTableEntityIDColumn string
}

// Set deletes any existing configuration for an entity and creates the given configuration.
// Empty configuration keys are skipped.
func (c *EntityConfigStore) Set(ctx context.Context, tx *sql.Tx, entityID int64, config map[string]string) error {
	var b strings.Builder
	b.WriteString("DELETE FROM ")
	b.WriteString(c.ConfigTable)
	b.WriteString(" WHERE ")
	b.WriteString(c.ConfigTableEntityIDColumn)
	b.WriteString(" = ?")
	_, err := tx.ExecContext(ctx, b.String(), entityID)
	if err != nil {
		return fmt.Errorf("Failed resetting entity configuration: %w", err)
	}

	keys := make([]string, 0, len(config))
	for k, v := range config {
		// Ignore empty values.
		if v == "" {
			continue
		}

		keys = append(keys, k)
	}

	if len(keys) == 0 {
		return nil
	}

	args := make([]any, 0, len(keys)*3)

	b.Reset()
	b.WriteString("INSERT INTO ")
	b.WriteString(c.ConfigTable)
	b.WriteString(" (")
	b.WriteString(c.ConfigTableEntityIDColumn)
	b.WriteString(", key, value) VALUES ")
	b.WriteString("(?, ?, ?)")
	args = append(args, entityID, keys[0], config[keys[0]])
	for _, key := range keys[1:] {
		b.WriteString(", (?, ?, ?)")
		args = append(args, entityID, key, config[key])
	}

	res, err := tx.ExecContext(ctx, b.String(), args...)
	if err != nil {
		return fmt.Errorf("Failed writing entity configuration: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed verifying entity configuration: %w", err)
	}

	if int(n) != len(keys) {
		return fmt.Errorf("Expected to write %d configuration entries but wrote %d", len(keys), n)
	}

	return nil
}

// GetByEntityID returns configuration for the entity with the given ID or an error if no entity exists with this ID.
func (c *EntityConfigStore) GetByEntityID(ctx context.Context, tx *sql.Tx, entityID int64) (map[string]string, error) {
	conf, err := c.GetByEntityIDs(ctx, tx, entityID)
	if err != nil {
		return nil, err
	}

	return conf[entityID], nil
}

// GetByEntityIDs returns configuration for entities with the given IDs or an error if one or more entities do not exist.
func (c *EntityConfigStore) GetByEntityIDs(ctx context.Context, tx *sql.Tx, entityIDs ...int64) (map[int64]map[string]string, error) {
	if len(entityIDs) == 0 {
		return make(map[int64]map[string]string), nil
	}

	b := new(strings.Builder)
	b.WriteString("WHERE ")
	b.WriteString(c.EntityTable)
	b.WriteString(".id IN (?")
	args := make([]any, 0, len(entityIDs))
	args = append(args, entityIDs[0])
	for _, id := range entityIDs[1:] {
		b.WriteString(", ?")
		args = append(args, id)
	}

	b.WriteString(")")
	entityConfigs, err := c.Select(ctx, tx, b.String(), args...)
	if err != nil {
		return nil, err
	}

	// Check if any IDs are missing.
	missingIDs := make([]string, 0, len(entityIDs))
	for _, entityID := range entityIDs {
		_, ok := entityConfigs[entityID]
		if !ok {
			missingIDs = append(missingIDs, strconv.FormatInt(entityID, 10))
		}
	}

	// If any IDs are missing there is an internal error (because IDs are internal).
	if len(missingIDs) > 0 {
		return nil, fmt.Errorf(`No rows in %q with ID %q`, c.EntityTable, strings.Join(missingIDs, `", "`))
	}

	return entityConfigs, nil
}

// GetAll returns all configuration available to the [EntityConfigStore].
func (c *EntityConfigStore) GetAll(ctx context.Context, tx *sql.Tx) (map[int64]map[string]string, error) {
	return c.Select(ctx, tx, "")
}

// Select gets all configuration for entities that match the given clause. The primary entity table is already part of the
// default query, so this can be joined to other tables if required. The IDs of all entities matching the given clause are
// returned. If the entity has no configuration, Select returns an empty map.
func (c *EntityConfigStore) Select(ctx context.Context, tx *sql.Tx, clause string, args ...any) (map[int64]map[string]string, error) {
	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(c.EntityTable)
	b.WriteString(".id, coalesce(")
	b.WriteString(c.ConfigTable)
	b.WriteString(".key, ''), coalesce(")
	b.WriteString(c.ConfigTable)
	b.WriteString(".value, '') FROM ")
	b.WriteString(c.EntityTable)
	b.WriteString(" LEFT JOIN ")
	b.WriteString(c.ConfigTable)
	b.WriteString(" ON ")
	b.WriteString(c.EntityTable)
	b.WriteString(".id = ")
	b.WriteString(c.ConfigTable)
	b.WriteString(".")
	b.WriteString(c.ConfigTableEntityIDColumn)
	b.WriteString(" ")
	b.WriteString(clause)

	configs := make(map[int64]map[string]string)
	err := Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		var entityID int64
		var key, value string
		err := scan(&entityID, &key, &value)
		if err != nil {
			return fmt.Errorf("Failed reading configuration: %w", err)
		}

		_, ok := configs[entityID]
		if !ok {
			configs[entityID] = make(map[string]string)
		}

		// Omit empty keys resulting from coalesced values.
		// This indicates that an entity with this ID exists but does not have any config.
		// For these entities we return an empty map.
		if key != "" {
			configs[entityID][key] = value
		}

		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed loading configuration: %w", err)
	}

	return configs, nil
}
