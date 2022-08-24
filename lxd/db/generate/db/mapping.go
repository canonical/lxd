package db

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/lxc/lxd/lxd/db/generate/lex"
	"github.com/lxc/lxd/shared"
)

// Mapping holds information for mapping database tables to a Go structure.
type Mapping struct {
	Package    string    // Package of the Go struct
	Name       string    // Name of the Go struct.
	Fields     []*Field  // Metadata about the Go struct.
	Filterable bool      // Whether the Go struct has a Filter companion struct for filtering queries.
	Filters    []*Field  // Metadata about the Go struct used for filter fields.
	Type       TableType // Type of table structure for this Go struct.

}

// TableType represents the logical type of the table defined by the Go struct.
type TableType int

// EntityTable represents the type for any entity that maps to a Go struct.
var EntityTable = TableType(0)

// ReferenceTable represents the type for for any entity that contains an
// 'entity_id' field mapping to a parent entity.
var ReferenceTable = TableType(1)

// AssociationTable represents the type for an entity that associates two
// other entities.
var AssociationTable = TableType(2)

// MapTable represents the type for a table storing key/value pairs.
var MapTable = TableType(3)

// NaturalKey returns the struct fields that can be used as natural key for
// uniquely identifying a row in the underlying table (==.
//
// By convention the natural key field is the one called "Name", unless
// specified otherwise with the `db:natural_key` tags.
func (m *Mapping) NaturalKey() []*Field {
	key := []*Field{}

	for _, field := range m.Fields {
		if field.Config.Get("primary") != "" {
			key = append(key, field)
		}
	}

	if len(key) == 0 {
		// Default primary key.
		key = append(key, m.FieldByName("Name"))
	}

	return key
}

// Identifier returns the field that uniquely identifies this entity.
func (m *Mapping) Identifier() *Field {
	for _, field := range m.NaturalKey() {
		if field.Name == "Name" || field.Name == "Fingerprint" {
			return field
		}
	}

	return nil
}

// ContainsFields checks that the mapping contains fields with the same type
// and name of given ones.
func (m *Mapping) ContainsFields(fields []*Field) bool {
	matches := map[*Field]bool{}

	for _, field := range m.Fields {
		for _, other := range fields {
			if field.Name == other.Name && field.Type.Name == other.Type.Name {
				matches[field] = true
			}
		}
	}

	return len(matches) == len(fields)
}

// FieldByName returns the field with the given name, if any.
func (m *Mapping) FieldByName(name string) *Field {
	for _, field := range m.Fields {
		if field.Name == name {
			return field
		}
	}

	return nil
}

// ActiveFilters returns the active filter fields for the kind of method.
func (m *Mapping) ActiveFilters(kind string) []*Field {
	names := activeFilters(kind)
	fields := []*Field{}
	for _, name := range names {
		field := m.FieldByName(name)
		if field != nil {
			fields = append(fields, field)
		}
	}
	return fields
}

// FieldColumnName returns the column name of the field with the given name,
// prefixed with the entity's table name.
func (m *Mapping) FieldColumnName(name string, table string) string {
	field := m.FieldByName(name)
	return fmt.Sprintf("%s.%s", table, field.Column())
}

// FilterFieldByName returns the field with the given name if that field can be
// used as query filter, an error otherwise.
func (m *Mapping) FilterFieldByName(name string) (*Field, error) {
	for _, filter := range m.Filters {
		if name == filter.Name {
			if filter.Type.Code != TypeColumn {
				return nil, fmt.Errorf("Unknown filter %q not a column", name)
			}

			return filter, nil
		}
	}

	return nil, fmt.Errorf("Unknown filter %q", name)
}

// ColumnFields returns the fields that map directly to a database column,
// either on this table or on a joined one.
func (m *Mapping) ColumnFields(exclude ...string) []*Field {
	fields := []*Field{}

	for _, field := range m.Fields {
		if shared.StringInSlice(field.Name, exclude) {
			continue
		}

		if field.Type.Code == TypeColumn {
			fields = append(fields, field)
		}
	}

	return fields
}

// ScalarFields returns the fields that map directly to a single database
// column on another table that can be joined to this one.
func (m *Mapping) ScalarFields() []*Field {
	fields := []*Field{}

	for _, field := range m.Fields {
		if field.Config.Get("join") != "" || field.Config.Get("leftjoin") != "" {
			fields = append(fields, field)
		}
	}

	return fields
}

// RefFields returns the fields that are one-to-many references to other
// tables.
func (m *Mapping) RefFields() []*Field {
	fields := []*Field{}

	for _, field := range m.Fields {
		if field.Type.Code == TypeSlice || field.Type.Code == TypeMap {
			fields = append(fields, field)
		}
	}

	return fields
}

// FieldArgs converts the given fields to function arguments, rendering their
// name and type.
func (m *Mapping) FieldArgs(fields []*Field, extra ...string) string {
	args := []string{}

	for _, field := range fields {
		name := lex.Minuscule(field.Name)
		if name == "type" {
			name = lex.Minuscule(m.Name) + field.Name
		}

		arg := fmt.Sprintf("%s %s", name, field.Type.Name)
		args = append(args, arg)
	}

	args = append(args, extra...)

	return strings.Join(args, ", ")
}

// FieldParams converts the given fields to function parameters, rendering their
// name.
func (m *Mapping) FieldParams(fields []*Field) string {
	args := make([]string, len(fields))
	for i, field := range fields {
		name := lex.Minuscule(field.Name)
		if name == "type" {
			name = lex.Minuscule(m.Name) + field.Name
		}

		args[i] = name
	}

	return strings.Join(args, ", ")
}

// Field holds all information about a field in a Go struct that is relevant
// for database code generation.
type Field struct {
	Name    string
	Type    Type
	Primary bool // Whether this field is part of the natural primary key.
	Config  url.Values
}

// Stmt must be used only on a non-columnar field. It returns the name of
// statement that should be used to fetch this field. A statement with that
// name must have been generated for the entity at hand.
func (f *Field) Stmt() string {
	switch f.Name {
	case "UsedBy":
		return "used_by"
	default:
		return ""
	}
}

// IsScalar returns true if the field is a scalar column value from a joined table.
func (f *Field) IsScalar() bool {
	return f.JoinConfig() != ""
}

// IsIndirect returns true if the field is a scalar column value from a joined
// table that in turn requires another join.
func (f *Field) IsIndirect() bool {
	return f.IsScalar() && f.Config.Get("via") != ""
}

// IsPrimary returns true if the field part of the natural key.
func (f *Field) IsPrimary() bool {
	return f.Config.Get("primary") != "" || f.Name == "Name"
}

// Column returns the name of the database column the field maps to. The type
// code of the field must be TypeColumn.
func (f *Field) Column() string {
	if f.Type.Code != TypeColumn {
		panic("attempt to get column name of non-column field")
	}

	column := lex.Snake(f.Name)

	join := f.JoinConfig()
	if join != "" {
		column = fmt.Sprintf("%s AS %s", join, column)
	}

	return column
}

func (f Field) JoinConfig() string {
	join := f.Config.Get("join")
	if join == "" {
		join = f.Config.Get("leftjoin")
	}

	return join
}

// ScalarTableColumn gets the table and column from the join configuration.
func (f Field) ScalarTableColumn() (string, string, error) {
	join := f.JoinConfig()

	if join == "" {
		return "", "", fmt.Errorf("Missing join config for field %q", f.Name)
	}

	joinFields := strings.Split(join, ".")
	if len(joinFields) != 2 {
		return "", "", fmt.Errorf("Join config must be of the format <table>.<column> for field %q", f.Name)
	}

	return joinFields[0], joinFields[1], nil
}

// FieldNames returns the names of the given fields.
func FieldNames(fields []*Field) []string {
	names := []string{}
	for _, f := range fields {
		names = append(names, f.Name)
	}

	return names
}

// Type holds all information about a field in a field type that is relevant
// for database code generation.
type Type struct {
	Name string
	Code int
}

// Possible type code.
const (
	TypeColumn = iota
	TypeSlice
	TypeMap
)
