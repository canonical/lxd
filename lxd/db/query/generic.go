package query

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// TableNamer is a type that reports the table that it lives in.
type TableNamer interface {
	TableName() string
}

// Creatable is a type that knows how to create itself.
type Creatable interface {
	TableNamer
	CreateStmt() string
	CreateValues() []any
}

// Referencable is a type that knows how to reference itself in the database.
// We need the table name and the column + value for the primary key.
type Referencable interface {
	TableNamer
	PKColumn() string
	PKValue() any
}

// Updatable defines a type that knows how to update itself.
type Updatable interface {
	Referencable
	UpdateStmt() string
	UpdateValues() []any
}

// BaseQuerier specifies how to query for all the basic data for a given type.
// The order of the columns should match the order from [ScanArger].
type BaseQuerier interface {
	BaseQuery() string
}

// ScanArger specifies how a type should advertise arguments for scanning rows.
// The order of these arguments should match the order of columns from [BaseQuerier].
type ScanArger interface {
	ScanArgs() []any
}

// Create creates a creatable. All columns are set except for the primary key.
func Create(ctx context.Context, tx *sql.Tx, c Creatable) (int64, error) {
	tableName := c.TableName()

	res, err := tx.ExecContext(ctx, c.CreateStmt(), c.CreateValues()...)
	if err != nil {
		if IsConflictErr(err) {
			return -1, api.StatusErrorf(http.StatusConflict, "This entry in table %q already exists: %w", tableName, err)
		}

		return -1, fmt.Errorf("Failed creating %q entry: %w", tableName, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed getting last inserted ID of new entry in table %q", tableName)
	}

	return id, nil
}

// Update updates an [Updatable] type by its primary key. It sets all fields except for the primary key.
// The expected usage is to get an [Updatable] entry from the database (e.g. via [SelectOne]), change fields on it
// directly, and then call Update.
func Update(ctx context.Context, tx *sql.Tx, u Updatable) error {
	tableName := u.TableName()

	res, err := tx.ExecContext(ctx, u.UpdateStmt(), u.UpdateValues()...)
	if err != nil {
		if IsConflictErr(err) {
			return api.StatusErrorf(http.StatusConflict, "This entry in table %q already exists: %w", tableName, err)
		}

		return fmt.Errorf("Failed updating %q entry: %w", tableName, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed verifying update to table %q entry: %w", tableName, err)
	}

	if n != 1 {
		return fmt.Errorf("Update by primary key where \"%s.%s = %v\" affected %d rows", tableName, u.PKColumn(), u.PKValue(), n)
	}

	return nil
}

// Select is a wrapper for [SelectFunc] which passes a simple function to append each scanned row to a slice and returns it.
func Select[T BaseQuerier, PT interface {
	ScanArger
	*T
}](ctx context.Context, tx *sql.Tx, clause string, args ...any) ([]T, error) {
	var ts []T
	f := func(t T) error {
		ts = append(ts, t)
		return nil
	}

	err := SelectFunc[T, PT](ctx, tx, clause, f, args...)
	if err != nil {
		return nil, err
	}

	return ts, nil
}

// SelectFunc selects data as defined by the base query of the given type.
// A function is called for every row scanned.
// Supplementary SQL can be passed in to specify additional joins, clauses, or aggregations.
// The type constraint specifies that the non-pointer type must implement BaseQuerier, but a pointer to the same type
// must implement [ScanArger].
func SelectFunc[T BaseQuerier, PT interface {
	ScanArger
	*T
}](ctx context.Context, tx *sql.Tx, clause string, rowFunc func(T) error, args ...any) error {
	rows, err := tx.QueryContext(ctx, (*new(T)).BaseQuery()+clause, args...)
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
// must implement [ScanArger].
func SelectOne[T BaseQuerier, PT interface {
	ScanArger
	*T
}](ctx context.Context, tx *sql.Tx, clause string, args ...any) (PT, error) {
	var pt PT
	f := func(t T) error {
		if pt != nil {
			return fmt.Errorf("More than one %T found", *(new(T)))
		}

		pt = &t
		return nil
	}

	err := SelectFunc[T, PT](ctx, tx, clause, f, args...)
	if err != nil {
		return nil, err
	}

	if pt == nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "%T not found", *(new(T)))
	}

	return pt, nil
}

// Delete deletes a [Referencable] type by primary key.
// The expected usage is to get a [Referencable] (e.g. via [SelectOne]) and then call Delete on it.
func Delete[T Referencable](ctx context.Context, tx *sql.Tx, t T) error {
	tableName := t.TableName()
	stmt := "DELETE FROM " + tableName + " WHERE " + t.PKColumn() + " = ?"
	result, err := tx.ExecContext(ctx, stmt, t.PKValue())
	if err != nil {
		return fmt.Errorf("Failed deleting from table %q: %w", tableName, err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed getting number of deleted rows: %w", err)
	}

	if deleted != 1 {
		return fmt.Errorf("Deletion by primary key where \"%s.%s = %v\" affected %d rows", t.TableName(), t.PKColumn(), t.PKValue(), deleted)
	}

	return nil
}

// DeleteMany deletes all rows from the table described by the given [TableNamer] that match the given clause.
// The type constraint enforces that both the type and a pointer to the type implement the [TableNamer] interface.
// This is the only way to enforce that (*new(T)) is a non-pointer type, and therefore non-nil.
func DeleteMany[T TableNamer, _ interface {
	TableNamer
	*T
}](ctx context.Context, tx *sql.Tx, clause string, args ...any) (int64, error) {
	tableName := (*new(T)).TableName()
	stmt := "DELETE FROM " + tableName + " " + clause
	result, err := tx.ExecContext(ctx, stmt, args...)
	if err != nil {
		return -1, fmt.Errorf("Failed deleting from table %q: %w", tableName, err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return -1, fmt.Errorf("Failed getting number of deleted rows: %w", err)
	}

	return n, nil
}

// DeleteOne deletes all rows from the table described by the given [TableNamer] that match the given clause.
// If the number of affected rows is not one, an error is returned (and the transaction should be rolled back).
// The type constraint enforces that both the type and a pointer to the type implement the [TableNamer] interface.
// This is the only way to enforce that (*new(T)) is a non-pointer type, and therefore non-nil.
func DeleteOne[T TableNamer, PT interface {
	TableNamer
	*T
}](ctx context.Context, tx *sql.Tx, clause string, args ...any) error {
	n, err := DeleteMany[T, PT](ctx, tx, clause, args...)
	if err != nil {
		return err
	}

	tableName := (*new(T)).TableName()
	if n < 1 {
		return api.StatusErrorf(http.StatusNotFound, "Failed to delete a single row from %q", tableName)
	} else if n > 1 {
		return fmt.Errorf("Expected to delete a single row from %q but deleted %d", tableName, n)
	}

	return nil
}
