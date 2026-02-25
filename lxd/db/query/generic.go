package query

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// TableNamer is a type that reports the table that it lives in.
type TableNamer interface {
	TableName() string
}

// Creatable is a type that knows how to create itself.
type Creatable interface {
	TableNamer
	CreateColumns() []string
	CreateValues() []any
}

// Create creates a creatable. All columns are set except for the primary key.
func Create(ctx context.Context, tx *sql.Tx, c Creatable) (int64, error) {
	return create(ctx, tx, c, false)
}

func create(ctx context.Context, tx *sql.Tx, c Creatable, replace bool) (int64, error) {
	tableName := c.TableName()

	var b strings.Builder
	b.WriteString("INSERT ")
	if replace {
		b.WriteString("OR REPLACE ")
	}

	b.WriteString("INTO " + tableName + "(" + strings.Join(c.CreateColumns(), ", ") + ") VALUES " + Params(len(c.CreateValues())))
	res, err := tx.ExecContext(ctx, b.String(), c.CreateValues()...)
	if err != nil {
		if IsConflictErr(err) {
			return -1, api.StatusErrorf(http.StatusConflict, "This entry in table %q already exists: %w", tableName, err)
		}

		return -1, fmt.Errorf("Failed to create %q entry: %w", tableName, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed to get last inserted ID of new entry in table %q", tableName)
	}

	return id, nil
}

// Updatable defines a type that knows how to update itself.
type Updatable interface {
	Referencable
	Creatable
}

// Update updates an Updatable type by its primary key. It sets all fields except for the primary key.
func Update(ctx context.Context, tx *sql.Tx, u Updatable) error {
	tableName := u.TableName()
	updateColumns := u.CreateColumns()
	updates := make([]string, 0, len(updateColumns))
	for _, col := range updateColumns {
		updates = append(updates, col+" = ?")
	}

	q := "UPDATE " + tableName + " SET " + strings.Join(updates, ", ") + " WHERE " + u.PKColumn() + " = ?"

	args := append(u.CreateValues(), u.PKValue())
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		if IsConflictErr(err) {
			return api.StatusErrorf(http.StatusConflict, "This entry in table %q already exists: %w", tableName, err)
		}

		return fmt.Errorf("Failed to update %q entry: %w", tableName, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to verify update to table %q entry: %w", tableName, err)
	}

	if n > 1 {
		return fmt.Errorf("Expected to update a single row of table %q by primary key but affected multiple", tableName)
	}

	if n < 1 {
		return api.StatusErrorf(http.StatusNotFound, "Expected to update a single row of table %q by primary key but no rows were affected", tableName)
	}

	return nil
}

// BaseQuerier specifies how to query for all the basic data for a given type.
// The order of the columns should match the order from ScanArger.
type BaseQuerier interface {
	BaseQuery() string
}

// ScanArger specifies how a type should advertise arguments for scanning rows.
// The order of these arguments should match the order of columns from BaseQuerier.
type ScanArger interface {
	ScanArgs() []any
}

// Select is a wrapper for SelectFunc which passes a simple function to append each scanned row to a slice and returns it.
func Select[T BaseQuerier, PT interface {
	ScanArger
	*T
}](ctx context.Context, tx *sql.Tx, supplementarySQL string, args ...any) ([]T, error) {
	var ts []T
	f := func(t T) error {
		ts = append(ts, t)
		return nil
	}

	err := SelectFunc[T, PT](ctx, tx, supplementarySQL, f, args...)
	if err != nil {
		return nil, err
	}

	return ts, nil
}

// SelectFunc selects data as defined by the base query of the given type.
// A function is called for every row scanned.
// Supplementary SQL can be passed in to specify additional joins, clauses, or aggregations.
// The type constraint specifies that the non-pointer type must implement BaseQuerier, but a pointer to the same type
// must implement ScanArger.
func SelectFunc[T BaseQuerier, PT interface {
	ScanArger
	*T
}](ctx context.Context, tx *sql.Tx, supplementarySQL string, rowFunc func(T) error, args ...any) error {
	rows, err := tx.QueryContext(ctx, (*new(T)).BaseQuery()+supplementarySQL, args...)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()
	for rows.Next() {
		pt := PT(new(T))
		err := rows.Scan(pt.ScanArgs()...)
		if err != nil {
			return err
		}

		err = rowFunc(*pt)
		if err != nil {
			return err
		}
	}

	return rows.Err()
}

// SelectOne selects a single row and errors if more than one row is found.
// Supplementary SQL can be passed in to specify additional joins, clauses, or aggregations.
// The type constraint specifies that the non-pointer type must implement BaseQuerier, but a pointer to the same type
// must implement ScanArger.
func SelectOne[T BaseQuerier, PT interface {
	ScanArger
	*T
}](ctx context.Context, tx *sql.Tx, supplementarySQL string, args ...any) (PT, error) {
	var pt PT
	f := func(t T) error {
		if pt != nil {
			return fmt.Errorf("More than one %T found", *(new(T)))
		}

		pt = &t
		return nil
	}

	err := SelectFunc[T, PT](ctx, tx, supplementarySQL, f, args...)
	if err != nil {
		return nil, err
	}

	if pt == nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "%T not found", *(new(T)))
	}

	return pt, nil
}

// Referencable is a type that knows how to reference itself in the database.
// We need the table name and the column + value for the primary key.
type Referencable interface {
	TableNamer
	PKColumn() string
	PKValue() any
}

// Delete deletes a Referencable type by primary key.
func Delete[T Referencable](ctx context.Context, tx *sql.Tx, t T) error {
	tableName := t.TableName()
	stmt := "DELETE FROM " + tableName + " WHERE " + t.PKColumn() + " = ?"
	result, err := tx.ExecContext(ctx, stmt, t.PKValue())
	if err != nil {
		return err
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to verify deletion from table %q: %w", tableName, err)
	}

	if n > 1 {
		return fmt.Errorf("Expected to delete a single row of table %q by primary key but affected multiple", tableName)
	}

	if n < 1 {
		return api.StatusErrorf(http.StatusNotFound, "Expected to delete a single row of table %q by primary key but no rows were affected", tableName)
	}

	return nil
}
