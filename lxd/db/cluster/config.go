//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t config.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e config objects
//go:generate mapper stmt -e config create struct=Config
//go:generate mapper stmt -e config delete
//
//go:generate mapper method -i -e config GetMany
//go:generate mapper method -i -e config Create struct=Config
//go:generate mapper method -i -e config Update struct=Config
//go:generate mapper method -i -e config DeleteMany
//go:generate goimports -w config.mapper.go
//go:generate goimports -w config.interface.mapper.go

// Config is a reference struct representing one configuration entry of another entity.
type Config struct {
	ID          int `db:"primary=yes"`
	ReferenceID int
	Key         string
	Value       string
}

// ConfigFilter specifies potential query parameter fields.
type ConfigFilter struct {
	Key   *string
	Value *string
}

// CreateEntityConfig inserts config rows for an entity. The table must have columns
// (<idColumn>, key, value). Empty values are skipped.
func CreateEntityConfig(ctx context.Context, tx *sql.Tx, table string, idColumn string, entityID int64, config map[string]string) error {
	stmt, err := tx.Prepare(fmt.Sprintf("INSERT INTO %s (%s, key, value) VALUES(?, ?, ?)", table, idColumn))
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.ExecContext(ctx, entityID, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}
