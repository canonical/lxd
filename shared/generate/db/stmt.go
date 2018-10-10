package db

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/lxc/lxd/shared/generate/file"
	"github.com/lxc/lxd/shared/generate/lex"
	"github.com/pkg/errors"
)

// Stmt generates a particular database query statement.
type Stmt struct {
	db       string                  // Target database (cluster or node)
	pkg      string                  // Package where the entity struct is declared.
	entity   string                  // Name of the database entity
	kind     string                  // Kind of statement to generate
	config   map[string]string       // Configuration parameters
	packages map[string]*ast.Package // Packages to perform for struct declaration lookups
}

// NewStmt return a new statement code snippet for running the given kind of
// query against the given database entity.
func NewStmt(database, pkg, entity, kind string, config map[string]string) (*Stmt, error) {
	packages, err := Packages()
	if err != nil {
		return nil, err
	}

	stmt := &Stmt{
		db:       database,
		pkg:      pkg,
		entity:   entity,
		kind:     kind,
		config:   config,
		packages: packages,
	}

	return stmt, nil
}

// Generate plumbing and wiring code for the desired statement.
func (s *Stmt) Generate(buf *file.Buffer) error {
	if strings.HasPrefix(s.kind, "objects") {
		return s.objects(buf)
	}

	if strings.HasPrefix(s.kind, "create") && strings.HasSuffix(s.kind, "-ref") {
		return s.createRef(buf)
	}

	if strings.HasSuffix(s.kind, "-ref") || strings.Contains(s.kind, "-ref-by-") {
		return s.ref(buf)
	}

	if strings.HasPrefix(s.kind, "names") {
		return s.names(buf)
	}

	switch s.kind {
	case "create":
		return s.create(buf)
	case "id":
		return s.id(buf)
	case "rename":
		return s.rename(buf)
	case "update":
		return s.update(buf)
	case "delete":
		return s.delete(buf)
	default:
		return fmt.Errorf("Unknown statement '%s'", s.kind)
	}
}

func (s *Stmt) objects(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return err
	}

	where := ""

	if strings.HasPrefix(s.kind, "objects-by") {
		filters := strings.Split(s.kind[len("objects-by-"):], "-and-")
		where = "WHERE "

		for i, filter := range filters {
			field, err := mapping.FilterFieldByName(filter)

			if err != nil {
				return err
			}

			if i > 0 {
				where += "AND "
			}

			var column string
			if field.IsScalar() {
				column = lex.Snake(field.Name)
			} else {
				column = mapping.FieldColumnName(field.Name)
			}

			where += fmt.Sprintf("%s = ? ", column)
		}

	}

	boiler := stmts["objects"]
	fields := mapping.ColumnFields()
	columns := make([]string, len(fields))
	for i, field := range fields {
		if field.IsScalar() {
			columns[i] = field.Column()
		} else {
			columns[i] = mapping.FieldColumnName(field.Name)
			coalesce, ok := field.Config["coalesce"]
			if ok {
				columns[i] = fmt.Sprintf("coalesce(%s, %s)", columns[i], coalesce[0])
			}
		}
	}
	nk := mapping.NaturalKey()
	orderBy := make([]string, len(nk))
	for i, field := range nk {
		if field.IsScalar() {
			orderBy[i] = lex.Plural(lex.Snake(field.Name)) + ".id"
		} else {
			orderBy[i] = mapping.FieldColumnName(field.Name)
		}
	}

	table := entityTable(s.entity)
	for _, field := range mapping.ScalarFields() {
		join := field.Config.Get("join")
		right := strings.Split(join, ".")[0]
		table += fmt.Sprintf(" JOIN %s ON %s_id = %s.id", right, lex.Singular(right), right)
	}

	sql := fmt.Sprintf(boiler, strings.Join(columns, ", "), table, where, strings.Join(orderBy, ", "))

	s.register(buf, sql)
	return nil
}

func (s *Stmt) names(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return err
	}

	table := entityTable(s.entity)
	for _, field := range mapping.ScalarFields() {
		join := field.Config.Get("join")
		right := strings.Split(join, ".")[0]
		table += fmt.Sprintf(" JOIN %s ON %s_id = %s.id", right, lex.Singular(right), right)
	}

	nk := mapping.NaturalKey()
	columns := make([]string, len(nk))
	orderBy := make([]string, len(nk))
	for i, field := range nk {
		if field.IsScalar() {
			columns[i] = field.Column()
			orderBy[i] = lex.Plural(lex.Snake(field.Name)) + ".id"
		} else {
			columns[i] = mapping.FieldColumnName(field.Name)
			orderBy[i] = mapping.FieldColumnName(field.Name)
		}
	}

	where := ""

	if strings.HasPrefix(s.kind, "names-by") {
		filters := strings.Split(s.kind[len("names-by-"):], "-and-")
		where = "WHERE "

		for i, filter := range filters {
			field, err := mapping.FilterFieldByName(filter)

			if err != nil {
				return err
			}

			if i > 0 {
				where += "AND "
			}

			var column string
			if field.IsScalar() {
				column = lex.Snake(field.Name)
			} else {
				column = mapping.FieldColumnName(field.Name)
			}

			where += fmt.Sprintf("%s = ? ", column)
		}

	}

	boiler := stmts["names"]
	sql := fmt.Sprintf(boiler, strings.Join(columns, ", "), table, where, strings.Join(orderBy, ", "))
	s.register(buf, sql)
	return nil
}

func (s *Stmt) ref(buf *file.Buffer) error {
	// Base snake-case name of the references (e.g. "used-by-ref" -> "used_by")
	name := strings.Replace(s.kind[:strings.Index(s.kind, "-ref")], "-", "_", -1)

	// Object type of the reference
	typ := lex.Camel(name)

	// Table name where reference objects can be fetched.
	table := fmt.Sprintf("%s_%s_ref", lex.Plural(s.entity), name)

	mapping, err := Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return err
	}

	field := mapping.FieldByName(typ)
	if field == nil {
		return fmt.Errorf("Entity %s has no field named %s", s.entity, typ)
	}

	nk := mapping.NaturalKey()

	var columns []string

	if IsColumnType(lex.Element(field.Type.Name)) {
		columns = make([]string, len(nk)+1)
		for i, field := range nk {
			columns[i] = lex.Snake(field.Name)
		}
		columns[len(columns)-1] = "value"
	} else if field.Type.Name == "map[string]string" {
		// By default we consider string->string maps of "config" type
		columns = make([]string, len(nk)+2)
		for i, field := range nk {
			columns[i] = lex.Snake(field.Name)
		}
		columns[len(columns)-2] = "key"
		columns[len(columns)-1] = "value"
	} else if field.Type.Name == "map[string]map[string]string" {
		// By default we consider string->map[string]string maps of "device" type
		columns = make([]string, len(nk)+4)
		for i, field := range nk {
			columns[i] = lex.Snake(field.Name)
		}
		columns[len(columns)-4] = "device"
		columns[len(columns)-3] = "type"
		columns[len(columns)-2] = "key"
		columns[len(columns)-1] = "value"
	} else {
		ref, err := Parse(s.packages["db"], typ)
		if err != nil {
			return errors.Wrap(err, "Parse referenced entity")
		}

		// Check that the reference object contains the primary key of the
		// entity.
		if !ref.ContainsFields(nk) {
			return fmt.Errorf("Reference type %s does not contain %s's primary key", typ, s.entity)
		}

		//columns = FieldColumns(ref.Fields)
	}

	where := ""
	if strings.Contains(s.kind, "-ref-by-") {
		filters := strings.Split(s.kind[strings.Index(s.kind, "-ref-by-")+len("-ref-by-"):], "-and-")
		where = "WHERE "

		for i, filter := range filters {
			field, err := mapping.FilterFieldByName(filter)

			if err != nil {
				return err
			}

			if i > 0 {
				where += "AND "
			}

			column := lex.Snake(field.Name)
			where += fmt.Sprintf("%s = ? ", column)
		}
	}

	orderBy := make([]string, len(nk))
	for i, field := range nk {
		orderBy[i] = lex.Snake(field.Name)
	}

	sql := fmt.Sprintf(
		"SELECT %s FROM %s %sORDER BY %s", strings.Join(columns, ", "),
		table, where, strings.Join(orderBy, ", "))

	s.register(buf, sql)

	return nil
}

func (s *Stmt) create(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := s.config["struct"]
	if !ok {
		entityCreate = entityPost(s.entity)
	}

	mapping, err := Parse(s.packages[s.pkg], entityCreate)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}
	fields := mapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.

	columns := make([]string, len(fields))
	params := make([]string, len(fields))

	for i, field := range fields {

		if field.IsScalar() {
			// TODO: make this more general
			ref := lex.Snake(field.Name)
			columns[i] = ref + "_id"
			params[i] = fmt.Sprintf("(SELECT id FROM %s WHERE name = ?)", lex.Plural(ref))
		} else {
			columns[i] = field.Column()
			params[i] = "?"
		}
	}

	sql := fmt.Sprintf(
		stmts[s.kind], entityTable(s.entity),
		strings.Join(columns, ", "), strings.Join(params, ", "))
	s.register(buf, sql)

	return nil
}

func (s *Stmt) createRef(buf *file.Buffer) error {
	// Base snake-case name of the references (e.g. "used-by-ref" -> "used_by")
	name := strings.Replace(s.kind[len("create-"):strings.Index(s.kind, "-ref")], "-", "_", -1)

	// Field name of the reference
	fieldName := lex.Camel(name)

	// Table name where reference objects can be fetched.
	table := fmt.Sprintf("%s_%s", entityTable(s.entity), name)

	mapping, err := Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return err
	}

	field := mapping.FieldByName(fieldName)
	if field == nil {
		return fmt.Errorf("Entity %s has no field named %s", s.entity, fieldName)
	}

	if field.Type.Name == "map[string]string" {
		// Assume this is a config table
		columns := fmt.Sprintf("%s_id, key, value", s.entity)
		params := "?, ?, ?"

		sql := fmt.Sprintf(stmts["create"], table, columns, params)
		s.register(buf, sql)

	} else if field.Type.Name == "map[string]map[string]string" {
		// Assume this is a devices table
		columns := fmt.Sprintf("%s_id, name, type", s.entity)
		params := "?, ?, ?"

		sql := fmt.Sprintf(stmts["create"], table, columns, params)
		s.register(buf, sql)

		columns = fmt.Sprintf("%s_device_id, key, value", s.entity)
		params = "?, ?, ?"

		sql = fmt.Sprintf(stmts["create"], table+"_config", columns, params)

		kind := fmt.Sprintf("Create%sConfigRef", field.Name)
		buf.L("var %s = %s.RegisterStmt(`\n%s\n`)", stmtCodeVar(s.entity, kind), s.db, sql)
	}

	return nil
}

func (s *Stmt) id(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}
	nk := mapping.NaturalKey()
	criteria := ""
	for i, field := range nk {
		if i > 0 {
			criteria += " AND "
		}

		var column string
		if field.IsScalar() {
			column = field.Config.Get("join")
		} else {
			column = mapping.FieldColumnName(field.Name)
		}

		criteria += fmt.Sprintf("%s = ?", column)
	}

	table := entityTable(s.entity)
	for _, field := range mapping.ScalarFields() {
		join := field.Config.Get("join")
		right := strings.Split(join, ".")[0]
		table += fmt.Sprintf(" JOIN %s ON %s_id = %s.id", right, lex.Singular(right), right)
	}

	sql := fmt.Sprintf(stmts[s.kind], entityTable(s.entity), table, criteria)
	s.register(buf, sql)

	return nil
}

func (s *Stmt) rename(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return err
	}

	table := entityTable(s.entity)

	nk := mapping.NaturalKey()
	where := make([]string, len(nk))

	for i, field := range nk {
		if field.IsScalar() {
			ref := lex.Snake(field.Name)
			where[i] = fmt.Sprintf("%s_id = (SELECT id FROM %s WHERE name = ?)", ref, lex.Plural(ref))
		} else {

			where[i] = fmt.Sprintf("%s = ?", field.Column())
		}

	}

	sql := fmt.Sprintf(stmts[s.kind], table, strings.Join(where, " AND "))
	s.register(buf, sql)
	return nil
}

func (s *Stmt) update(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityUpdate, ok := s.config["struct"]
	if !ok {
		entityUpdate = entityPut(s.entity)
	}

	mapping, err := Parse(s.packages[s.pkg], entityUpdate)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}
	fields := mapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.

	updates := make([]string, len(fields))

	for i, field := range fields {
		if field.IsScalar() {
			// TODO: make this more general
			ref := lex.Snake(field.Name)
			updates[i] = fmt.Sprintf("%s_id = ", ref)
			updates[i] += fmt.Sprintf("(SELECT id FROM %s WHERE name = ?)", lex.Plural(ref))
		} else {
			updates[i] = fmt.Sprintf("%s = ?", field.Column())
		}
	}

	mapping, err = Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	nk := mapping.NaturalKey()
	where := make([]string, len(nk))

	for i, field := range nk {
		if field.IsScalar() {
			ref := lex.Snake(field.Name)
			where[i] = fmt.Sprintf("%s_id = (SELECT id FROM %s WHERE name = ?)", ref, lex.Plural(ref))
		} else {

			where[i] = fmt.Sprintf("%s = ?", field.Column())
		}

	}

	sql := fmt.Sprintf(
		stmts[s.kind], entityTable(s.entity),
		strings.Join(updates, ", "), strings.Join(where, ", "))
	s.register(buf, sql)

	return nil
}

func (s *Stmt) delete(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Capital(s.entity))
	if err != nil {
		return err
	}

	table := entityTable(s.entity)

	nk := mapping.NaturalKey()
	where := make([]string, len(nk))

	for i, field := range nk {
		if field.IsScalar() {
			ref := lex.Snake(field.Name)
			where[i] = fmt.Sprintf("%s_id = (SELECT id FROM %s WHERE name = ?)", ref, lex.Plural(ref))
		} else {

			where[i] = fmt.Sprintf("%s = ?", field.Column())
		}

	}

	sql := fmt.Sprintf(stmts[s.kind], table, strings.Join(where, " AND "))
	s.register(buf, sql)
	return nil
}

// Output a line of code that registers the given statement and declares the
// associated statement code global variable.
func (s *Stmt) register(buf *file.Buffer, sql string, filters ...string) {
	kind := strings.Replace(s.kind, "-", "_", -1)
	if kind == "id" {
		kind = "ID" // silence go lints
	}
	buf.L("var %s = %s.RegisterStmt(`\n%s\n`)", stmtCodeVar(s.entity, kind, filters...), s.db, sql)
}

// Map of boilerplate statements.
var stmts = map[string]string{
	"names":   "SELECT %s\n  FROM %s\n  %sORDER BY %s",
	"objects": "SELECT %s\n  FROM %s\n  %sORDER BY %s",
	"create":  "INSERT INTO %s (%s)\n  VALUES (%s)",
	"id":      "SELECT %s.id FROM %s\n  WHERE %s",
	"rename":  "UPDATE %s SET name = ? WHERE %s",
	"update":  "UPDATE %s\n  SET %s\n WHERE %s",
	"delete":  "DELETE FROM %s WHERE %s",
}
