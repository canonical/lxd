package main

import (
	"fmt"
	"strings"
	"text/template"
	"unicode"

	"github.com/canonical/lxd/lxd/db/query"
)

// Spec defines how an inspected struct maps to the database.
type Spec struct {
	Reference          *Spec
	ReferenceFieldName string
	StructName         string
	TableName          string
	Fields             []FieldSpec
	PrimaryKey         FieldSpec
	Joins              []string
	ConfigTableName    string
	ConfigForeignKey   string
}

// FieldSpec is a simple mapping of struct field name to database column name.
type FieldSpec struct {
	FieldName  string
	ColumnName string
}

// templateContext returns the values used for rendering the template.
func (s *Spec) templateContext() map[string]any {
	return map[string]any{
		"receiver":        string(s.receiver()),
		"structName":      s.StructName,
		"tableName":       s.TableName,
		"backtick":        "`",
		"scanColumns":     s.scanColumns(),
		"joins":           s.joins(),
		"scanArgs":        s.scanArgs(),
		"createValues":    s.createValues(),
		"pkColumn":        s.pkColumn(),
		"pkValue":         s.pkValue(),
		"createStmt":      s.createStmt(),
		"updateStmt":      s.updateStmt(),
		"genAPIName":      s.Reference != nil,
		"apiName":         s.apiName(),
		"genConfig":       s.ConfigTableName != "",
		"configTableName": s.configTableName(),
	}
}

var specSelectTemplate = template.Must(template.New("select").Parse(`
// TableName returns the table name for [{{ .structName }}] entities.
func ({{ .receiver }} {{ .structName }}) TableName() string {
	return "{{ .tableName }}"
}
{{ if .genAPIName }}
// APIName implements [query.APINamer] for API friendly error messages.
func ({{ .receiver }} {{ .structName }}) APIName() string {
	return {{ .apiName }}
}
{{ end }}
// SelectColumns returns a slice of column names for [{{ .structName }}] entities.
func ({{ .receiver }} {{ .structName }}) SelectColumns() []string {
	return {{ .scanColumns }}
}

// Joins returns a slice of join expressions for [{{ .structName }}].
func ({{ .receiver }} {{ .structName }}) Joins() []string {
	return {{ .joins }}
}

// ScanArgs implements [query.ScanArger] for [{{ .structName }}].
// This returns references to struct fields in definition order.
func ({{ .receiver }} *{{ .structName }}) ScanArgs() []any {
	return {{ .scanArgs }}
}
{{ if .genConfig }}
// APIName implements [query.APINamer] for API friendly error messages.
func ({{ .receiver }} {{ .structName }}) ConfigTable() (configTable string, foreignKey string) {
	return {{ .configTableName }}
}
{{ end }}
`))

var specExecTemplate = template.Must(template.New("exec").Parse(`
// CreateValues returns a list of values from [{{ .structName }}] entities matching the columns returned from CreateColumns.
func ({{ .receiver }} {{ .structName }}) CreateValues() []any {
	return {{ .createValues }}
}

// PKColumn returns the column name for the primary key of a [{{ .structName }}] entity used during an update.
func ({{ .receiver }} {{ .structName }}) PKColumn() string {
	return {{ .pkColumn }}
}

// PKValue returns the value for the primary key of a [{{ .structName }}] entity used during an update.
func ({{ .receiver }} {{ .structName }}) PKValue() int64 {
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
`))

func (s *Spec) apiName() string {
	if s.Reference == nil {
		return ""
	}

	return string(s.receiver()) + "." + s.ReferenceFieldName + ".APIName()"
}

func (s *Spec) receiver() rune {
	return unicode.ToLower(rune(s.StructName[0]))
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

	return "UPDATE " + s.TableName + " SET " + strings.Join(updates, ", ") + " "
}

func (s *Spec) joins() string {
	if len(s.Joins) == 0 {
		return "[]string{}"
	}

	return "[]string{\n\t\t\"" + strings.Join(s.Joins, "\",\n\t\t\"") + "\",\n\t}"
}

func (s *Spec) scanColumns() string {
	cols := make([]string, 0, len(s.Fields))
	if s.Reference != nil {
		cols = make([]string, 0, len(s.Fields)+len(s.Reference.Fields))
		for _, f := range s.Reference.Fields {
			cols = append(cols, f.ColumnName)
		}
	}

	for _, f := range s.Fields {
		cols = append(cols, f.ColumnName)
	}

	return "[]string{\n\t\t\"" + strings.Join(cols, "\",\n\t\t\"") + "\",\n\t}"
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
	if s.Reference != nil {
		scanArgs = make([]string, 0, len(s.Reference.Fields)+len(s.Fields))
		for _, f := range s.Reference.Fields {
			scanArgs = append(scanArgs, "&"+string(s.receiver())+"."+s.ReferenceFieldName+"."+f.FieldName)
		}
	}

	for _, f := range s.Fields {
		scanArgs = append(scanArgs, "&"+string(s.receiver())+"."+f.FieldName)
	}

	return "[]any{" + strings.Join(scanArgs, ", ") + "}"
}

func (s *Spec) configTableName() string {
	return fmt.Sprintf("%q, %q", s.ConfigTableName, s.ConfigForeignKey)
}
