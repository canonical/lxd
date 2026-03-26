package query

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// APINamer is used by the generic functions below to return API friendly error messages.
type APINamer interface {
	APIName() string
}

// APIPluralNamer can be optionally implemented by types implementing [APINamer] to fully specify the pluralised form.
// Otherwise, all names will be pluralised with an (s) suffix.
// This is used for e.g. Identity -> Identities (rather than Identitys).
type APIPluralNamer interface {
	APIPluralName() string
}

// TableNamer is a type that reports the table that it lives in.
type TableNamer interface {
	TableName() string
}

// CreateValuer defines the values used at create time for the implementing type.
// The primary key should not be included, as it is expected that this is an auto-incrementing integer value to be
// returned by the database.
type CreateValuer interface {
	CreateValues() []any
}

// Creatable is a type that knows how to create itself.
type Creatable interface {
	APINamer
	TableNamer
	CreateValuer
	CreateStmt() string
}

// Referenceable is a type that knows how to reference itself in the database.
// We need the table name and the column + value for the primary key.
type Referenceable interface {
	APINamer
	TableNamer
	PKColumn() string
	PKValue() any
}

// Updatable defines a type that knows how to update itself.
type Updatable interface {
	Referenceable
	CreateValuer
	UpdateStmt() string
}

// Selectable specifies how to query for all the basic data for a given type.
// The order of the columns in SelectColumns should match the order from [ScanArger].
type Selectable interface {
	APINamer
	TableNamer
	SelectColumns() []string
	Joins() []string
}

// ScanArger specifies how a type should advertise arguments for scanning rows.
// It must be implemented on a pointer to a [Selectable], and the order of the arguments must
// match the order of columns from [Selectable.SelectColumns].
type ScanArger interface {
	ScanArgs() []any
}

// notFoundErr returns a 404 Not Found error for the given type.
func notFoundErr(t APINamer) error {
	return api.NewStatusError(http.StatusNotFound, t.APIName()+" not found")
}

// conflictErr returns a 409 Conflict error for the given type.
func conflictErr(t APINamer) error {
	return api.NewStatusError(http.StatusConflict, t.APIName()+" already exists")
}

// plural returns the pluralised form of the [APINamer].
func plural(t APINamer) string {
	pluralNamer, ok := any(t).(APIPluralNamer)
	if ok {
		return pluralNamer.APIPluralName()
	}

	return t.APIName() + "s"
}

// Create creates a [Creatable]. All columns are set except for the primary key.
// This is because it is assumed that the primary key is auto-assigned at the database layer.
func Create(ctx context.Context, tx *sql.Tx, c Creatable) (int64, error) {
	res, err := tx.ExecContext(ctx, c.CreateStmt(), c.CreateValues()...)
	if err != nil {
		if IsConflictErr(err) {
			return -1, conflictErr(c)
		}

		return -1, fmt.Errorf("Failed creating %s: %w", strings.ToLower(c.APIName()), err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed getting ID of new %s: %w", strings.ToLower(c.APIName()), err)
	}

	return id, nil
}

// UpdateByPrimaryKey updates an [Updatable] type by its primary key. It sets all fields except for the primary key.
// The expected usage is to get an [Updatable] entry from the database (e.g. via [SelectOne]), change fields on it
// directly, and then call Update. This pattern is encouraged so that we get an entity, perform an authorization check,
// and then update the values.
func UpdateByPrimaryKey(ctx context.Context, tx *sql.Tx, u Updatable) error {
	tableName := u.TableName()

	var b strings.Builder
	b.WriteString(u.UpdateStmt())
	b.WriteString(" WHERE ")
	b.WriteString(u.PKColumn())
	b.WriteString(" = ?")

	res, err := tx.ExecContext(ctx, b.String(), append(u.CreateValues(), u.PKValue())...)
	if err != nil {
		if IsConflictErr(err) {
			return conflictErr(u)
		}

		return fmt.Errorf("Failed updating %s: %w", strings.ToLower(u.APIName()), err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed verifying %s update: %w", strings.ToLower(u.APIName()), err)
	}

	if n != 1 {
		return fmt.Errorf("Update by primary key where `%s.%s = %v` affected %d rows", tableName, u.PKColumn(), u.PKValue(), n)
	}

	return nil
}

// UpdateOne updates a single [Updatable] that matches the given clause. It sets all fields except for the primary key.
// The args variadic should contain only the bind arguments for the given clause.
// Remaining bind arguments are defined by the [Updatable] type.
func UpdateOne(ctx context.Context, tx *sql.Tx, u Updatable, clause string, args ...any) error {
	var b strings.Builder
	b.WriteString(u.UpdateStmt())
	b.WriteString(" ")
	b.WriteString(clause)

	res, err := tx.ExecContext(ctx, b.String(), append(u.CreateValues(), args...)...)
	if err != nil {
		if IsConflictErr(err) {
			return conflictErr(u)
		}

		return fmt.Errorf("Failed updating %s: %w", strings.ToLower(u.APIName()), err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed verifying %s update: %w", strings.ToLower(u.APIName()), err)
	}

	if n < 1 {
		return notFoundErr(u)
	} else if n > 1 {
		return fmt.Errorf("Expected to update one %s but updated %d", strings.ToLower(u.APIName()), n)
	}

	return nil
}

// Select is a wrapper for [SelectFunc] which passes a simple function to append each scanned row to a slice and returns it.
func Select[T Selectable, PT interface {
	ScanArgs() []any
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

// SelectFunc selects data as defined by the base query of the given type. A function is called for every row scanned.
// Supplementary SQL can be passed in to specify additional joins, clauses, or aggregations.
// The type constraint specifies that the non-pointer type must implement BaseQuerier, but a pointer to the same type
// must implement [ScanArger]. This is important because it means that new(T), a pointer to T, is non-nil.
// It also means that we call ScanArgs on the pointer to the type, so we scan a row directly into each fields' memory.
func SelectFunc[T Selectable, PT interface {
	ScanArgs() []any
	*T
}](ctx context.Context, tx *sql.Tx, clause string, rowFunc func(T) error, args ...any) error {
	t := *new(T)

	var b strings.Builder
	b.WriteString("SELECT ")
	columns := t.SelectColumns()
	if len(columns) == 0 {
		return fmt.Errorf("%T implements Selectable but SelectColumns has length 0", t)
	}

	b.WriteString(columns[0])
	for _, column := range columns[1:] {
		b.WriteString(", ")
		b.WriteString(column)
	}

	b.WriteString(" FROM ")
	b.WriteString(t.TableName())
	joins := t.Joins()
	for _, join := range joins {
		b.WriteString(" ")
		b.WriteString(join)
	}

	b.WriteString(" ")
	b.WriteString(clause)

	rows, err := tx.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return fmt.Errorf("Failed loading %s: %w", strings.ToLower(plural(t)), err)
	}

	defer func() { _ = rows.Close() }()
	for rows.Next() {
		pt := PT(new(T))
		err := rows.Scan(pt.ScanArgs()...)
		if err != nil {
			return fmt.Errorf("Failed reading %s: %w", strings.ToLower(t.APIName()), err)
		}

		err = rowFunc(*pt)
		if err != nil {
			return err
		}
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Failed loading %s: %w", strings.ToLower(plural(t)), rows.Err())
	}

	return nil
}

// SelectOne selects a single row and errors if more than one row is found.
// Supplementary SQL can be passed in to specify additional joins, clauses, or aggregations.
// The type constraint specifies that the non-pointer type must implement BaseQuerier, but a pointer to the same type
// must implement [ScanArger].
func SelectOne[T Selectable, PT interface {
	ScanArgs() []any
	*T
}](ctx context.Context, tx *sql.Tx, clause string, args ...any) (PT, error) {
	var pt PT
	f := func(t T) error {
		if pt != nil {
			return fmt.Errorf("More than one %s found", strings.ToLower(t.APIName()))
		}

		pt = &t
		return nil
	}

	err := SelectFunc[T, PT](ctx, tx, clause, f, args...)
	if err != nil {
		return nil, err
	}

	if pt == nil {
		return nil, notFoundErr(*new(T))
	}

	return pt, nil
}

// DeleteByPrimaryKey deletes a [Referenceable] type by primary key.
// The expected usage is to get a [Referenceable] (e.g. via [SelectOne]) and then call Delete on it.
// Note that this function does not call DeleteOne. This is because the Referenceable argument might
// not satisfy the type constraint.
func DeleteByPrimaryKey[T Referenceable](ctx context.Context, tx *sql.Tx, t T) error {
	tableName := t.TableName()

	var b strings.Builder
	b.WriteString("DELETE FROM ")
	b.WriteString(tableName)
	b.WriteString(" WHERE ")
	b.WriteString(t.PKColumn())
	b.WriteString(" = ?")

	result, err := tx.ExecContext(ctx, b.String(), t.PKValue())
	if err != nil {
		return fmt.Errorf("Failed deleting from table %q: %w", tableName, err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed getting number of deleted rows: %w", err)
	}

	if deleted != 1 {
		return fmt.Errorf("Deletion by primary key where `%s.%s = %v` affected %d rows", t.TableName(), t.PKColumn(), t.PKValue(), deleted)
	}

	return nil
}

// DeleteMany deletes all rows from the table described by the given [TableNamer] that match the given clause.
// The type constraint enforces that both the type and a pointer to the type implement the [TableNamer] interface.
// This is the only way to enforce that (*new(T)) is a non-pointer type, and therefore non-nil.
func DeleteMany[T Referenceable, _ interface {
	Referenceable
	*T
}](ctx context.Context, tx *sql.Tx, clause string, args ...any) (int64, error) {
	tableName := (*new(T)).TableName()

	var b strings.Builder
	b.WriteString("DELETE FROM ")
	b.WriteString(tableName)
	b.WriteString(" ")
	b.WriteString(clause)

	result, err := tx.ExecContext(ctx, b.String(), args...)
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
func DeleteOne[T Referenceable, PT interface {
	Referenceable
	*T
}](ctx context.Context, tx *sql.Tx, clause string, args ...any) error {
	n, err := DeleteMany[T, PT](ctx, tx, clause, args...)
	if err != nil {
		return err
	}

	t := *new(T)
	tableName := t.TableName()
	if n < 1 {
		return notFoundErr(t)
	} else if n > 1 {
		return fmt.Errorf("Expected to delete a single row from %q but deleted %d", tableName, n)
	}

	return nil
}
