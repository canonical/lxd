package main

import (
	"strings"
	"text/template"
	"unicode"

	"github.com/canonical/lxd/lxd/db/query"
)

// Spec defines how an inspected struct maps to the database.
type Spec struct {
	StructName string
	TableName  string
	Fields     []FieldSpec
	PrimaryKey FieldSpec
	Joins      []string
}

// FieldSpec is a simple mapping of struct field name to database column name.
type FieldSpec struct {
	FieldName  string
	ColumnName string
}

// templateContext returns the values used for rendering the template.
func (s *Spec) templateContext() map[string]any {
	return map[string]any{
		"receiver":     string(s.receiver()),
		"structName":   s.StructName,
		"tableName":    s.TableName,
		"backtick":     "`",
		"baseQuery":    s.baseQuery(),
		"scanColumns":  s.scanColumns(),
		"scanArgs":     s.scanArgs(),
		"createValues": s.createValues(),
		"pkColumn":     s.pkColumn(),
		"pkValue":      s.pkValue(),
		"createStmt":   s.createStmt(),
		"updateStmt":   s.updateStmt(),
	}
}

var specTemplate = template.Must(template.New("spec").Parse(`
// TableName returns the table name for [{{ .structName }}] entities.
func ({{ .receiver }} {{ .structName }}) TableName() string {
	return "{{ .tableName }}"
}

// ScanColumns returns a slice of column names for [{{ .structName }}] entities.
func ({{ .receiver }} {{ .structName }}) ScanColumns() []string {
	return {{ .scanColumns }}
}

// BaseQuery implements [query.BaseQuerier] for [{{ .structName }}].
// Query columns appear in field definition order.
func ({{ .receiver }} {{ .structName }}) BaseQuery() string {
	return {{ .backtick }}{{ .baseQuery }}{{ .backtick }}
}

// ScanArgs implements [query.ScanArger] for [{{ .structName }}].
// This returns references to struct fields in definition order.
func ({{ .receiver }} *{{ .structName }}) ScanArgs() []any {
	return {{ .scanArgs }}
}

// CreateValues returns a list of values from [{{ .structName }}] entities matching the columns returned from CreateColumns.
func ({{ .receiver }} {{ .structName }}) CreateValues() []any {
	return {{ .createValues }}
}

// PKColumn returns the column name for the primary key of a [{{ .structName }}] entity used during an update.
func ({{ .receiver }} {{ .structName }}) PKColumn() string {
	return {{ .pkColumn }}
}

// PKValue returns the value for the primary key of a [{{ .structName }}] entity used during an update.
func ({{ .receiver }} {{ .structName }}) PKValue() any {
	return {{ .pkValue }}
}

// CreateStmt returns a query that creates a [{{ .structName }}] entity.
func ({{ .receiver }} {{ .structName }}) CreateStmt() string {
	return "{{ .createStmt }}"
}

// UpdateStmt returns a query that updates a [{{ .structName }}] by primary key.
func ({{ .receiver }} {{ .structName }}) UpdateStmt() string {
	return "{{ .updateStmt }}"
}

// UpdateValues returns a list of values from [{{ .structName }}] entities to be used when updating a row by primary key.
func ({{ .receiver }} {{ .structName }}) UpdateValues() []any {
    return append({{ .receiver }}.CreateValues(), {{ .receiver }}.PKValue())
}
`))

func (s *Spec) receiver() rune {
	return unicode.ToLower(rune(s.StructName[0]))
}

func (s *Spec) baseQuery() string {
	var b strings.Builder
	b.WriteString("SELECT\n\t")
	b.WriteString(strings.Join(s.scanCols(), ",\n\t"))
	b.WriteString("\nFROM " + s.TableName + "\n")
	for _, join := range s.Joins {
		b.WriteString(join + "\n")
	}

	return b.String()
}

func (s *Spec) createStmt() string {
	cols := s.createColumns()
	return "INSERT INTO " + s.TableName + " (" + strings.Join(cols, ", ") + ") VALUES " + query.Params(len(cols))
}

func (s *Spec) updateStmt() string {
	updateColumns := s.createColumns()
	updates := make([]string, 0, len(updateColumns))
	for _, col := range updateColumns {
		updates = append(updates, col+" = ?")
	}

	return "UPDATE " + s.TableName + " SET " + strings.Join(updates, ", ") + " WHERE " + s.PrimaryKey.ColumnName + " = ?"
}

func (s *Spec) scanCols() []string {
	cols := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		cols = append(cols, f.ColumnName)
	}

	return cols
}

func (s *Spec) scanColumns() string {
	return "[]string{\n\t\t\"" + strings.Join(s.scanCols(), "\",\n\t\t\"") + "\",\n\t}"
}

func (s *Spec) createColumns() []string {
	cols := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		unqualifiedColName, ok := strings.CutPrefix(f.ColumnName, s.TableName+".")
		if !ok {
			continue
		}

		if f == s.PrimaryKey {
			continue
		}

		cols = append(cols, unqualifiedColName)
	}

	return cols
}

func (s *Spec) createValues() string {
	values := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		if !strings.HasPrefix(f.ColumnName, s.TableName+".") {
			continue
		}

		if f == s.PrimaryKey {
			continue
		}

		values = append(values, string(s.receiver())+"."+f.FieldName)
	}

	return "[]any{" + strings.Join(values, ", ") + "}"
}

func (s *Spec) pkColumn() string {
	return `"` + strings.TrimPrefix(s.PrimaryKey.ColumnName, s.TableName+".") + `"`
}

func (s *Spec) pkValue() string {
	return string(s.receiver()) + "." + s.PrimaryKey.FieldName
}

func (s *Spec) scanArgs() string {
	scanArgs := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		scanArgs = append(scanArgs, "&"+string(s.receiver())+"."+f.FieldName)
	}

	return "[]any{" + strings.Join(scanArgs, ", ") + "}"
}
