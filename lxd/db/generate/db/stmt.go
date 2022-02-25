package db

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/lxc/lxd/lxd/db/generate/file"
	"github.com/lxc/lxd/lxd/db/generate/lex"
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
	kind := strings.Split(s.kind, "-by-")[0]

	switch kind {
	case "objects":
		return s.objects(buf)
	case "delete":
		return s.delete(buf)
	case "create":
		return s.create(buf, false)
	case "create-or-replace":
		return s.create(buf, true)
	case "id":
		return s.id(buf)
	case "rename":
		return s.rename(buf)
	case "update":
		return s.update(buf)
	default:
		return fmt.Errorf("Unknown statement '%s'", s.kind)
	}
}

// GenerateSignature is not used for statements
func (s *Stmt) GenerateSignature(buf *file.Buffer) error {
	return nil
}

func (s *Stmt) objects(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Camel(s.entity), s.kind)
	if err != nil {
		return err
	}

	table := entityTable(s.entity)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		table = "%s_" + table
	}

	where := ""

	if strings.HasPrefix(s.kind, "objects-by") {
		filters := strings.Split(s.kind[len("objects-by-"):], "-and-")
		where = "WHERE "

		for i, filter := range filters {
			if i > 0 {
				where += "AND "
			}

			if filter == "Parent" {
				where += fmt.Sprintf("SUBSTR(%s.name,1,?)=? ", lex.Plural(s.entity))
				continue
			}

			field, err := mapping.FilterFieldByName(filter)
			if err != nil {
				return err
			}

			var column string
			if field.IsScalar() {
				column = lex.Snake(field.Name)
			} else {
				column = mapping.FieldColumnName(field.Name, table)
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

			coalesce, ok := field.Config["coalesce"]
			if ok {
				// Handle columns in format "<field> AS <alias>".
				parts := strings.SplitN(columns[i], " ", 2)
				columns[i] = fmt.Sprintf("coalesce(%s, %s)", parts[0], coalesce[0])

				if len(parts) > 1 {
					columns[i] = fmt.Sprintf("%s %s", columns[i], parts[1])
				}
			}
		} else {
			columns[i] = mapping.FieldColumnName(field.Name, table)
			if mapping.Type == ReferenceTable || mapping.Type == MapTable {
				columns[i] = strings.Replace(columns[i], "reference", "%s", -1)
			}

			coalesce, ok := field.Config["coalesce"]
			if ok {
				columns[i] = fmt.Sprintf("coalesce(%s, %s)", columns[i], coalesce[0])
			}
		}
	}
	orderBy := []string{}
	for _, field := range fields {
		if field.Config.Get("order") != "" {
			if field.IsScalar() {
				orderBy = append(orderBy, lex.Plural(lex.Snake(field.Name))+".id")
			} else {
				line := mapping.FieldColumnName(field.Name, table)
				if mapping.Type == ReferenceTable || mapping.Type == MapTable {
					line = strings.Replace(line, "reference", "%s", -1)
				}

				orderBy = append(orderBy, line)
			}
		}
	}

	if len(orderBy) < 1 {
		nk := mapping.NaturalKey()
		orderBy = make([]string, len(nk))
		for i, field := range nk {
			if field.IsScalar() {
				orderBy[i] = lex.Plural(lex.Snake(field.Name)) + ".id"
			} else {
				orderBy[i] = mapping.FieldColumnName(field.Name, table)
				if mapping.Type == ReferenceTable || mapping.Type == MapTable {
					orderBy[i] = strings.Replace(orderBy[i], "reference", "%s", -1)
				}
			}
		}
	}

	for _, field := range mapping.ScalarFields() {
		join := field.Config.Get("join")
		if join == "" {
			continue
		}

		right := strings.Split(join, ".")[0]
		via := entityTable(s.entity)
		if field.Config.Get("via") != "" {
			via = entityTable(field.Config.Get("via"))
		}
		table += fmt.Sprintf(" JOIN %s ON %s.%s_id = %s.id", right, via, lex.Singular(right), right)
	}

	for _, field := range mapping.ScalarFields() {
		join := field.Config.Get("leftjoin")
		if join == "" {
			continue
		}

		right := strings.Split(join, ".")[0]
		via := entityTable(s.entity)
		if field.Config.Get("via") != "" {
			via = entityTable(field.Config.Get("via"))
		}
		table += fmt.Sprintf(" LEFT JOIN %s ON %s.%s_id = %s.id", right, via, lex.Singular(right), right)
	}

	sql := fmt.Sprintf(boiler, strings.Join(columns, ", "), table, where, strings.Join(orderBy, ", "))
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("const %s = `%s`", stmtName, sql)
	} else {
		s.register(buf, stmtName, sql)
	}
	return nil
}

func (s *Stmt) create(buf *file.Buffer, replace bool) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := s.config["struct"]
	if !ok {
		entityCreate = entityPost(s.entity)
	}

	mapping, err := Parse(s.packages[s.pkg], entityCreate, s.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}
	all := mapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.
	via := map[string][]*Field{}      // Map scalar fields to their additional indirect fields

	// Filter out indirect fields
	fields := []*Field{}
	for _, field := range all {
		if field.IsIndirect() {
			entity := field.Config.Get("via")
			via[entity] = append(via[entity], field)
			continue
		}
		fields = append(fields, field)
	}

	columns := make([]string, len(fields))
	params := make([]string, len(fields))

	for i, field := range fields {

		if field.IsScalar() {
			ref := lex.Snake(field.Name)
			columns[i] = ref + "_id"
			table := entityTable(ref)
			params[i] = fmt.Sprintf("(SELECT %s.id FROM %s", table, table)
			for _, other := range via[ref] {
				otherRef := lex.Snake(other.Name)
				otherTable := entityTable(otherRef)
				params[i] += fmt.Sprintf(" JOIN %s ON %s.id = %s.%s_id", otherTable, otherTable, table, otherRef)
			}
			params[i] += fmt.Sprintf(" WHERE")
			for _, other := range via[ref] {
				otherRef := lex.Snake(other.Name)
				otherTable := entityTable(otherRef)
				params[i] += fmt.Sprintf(" %s.name = ? AND", otherTable)
			}
			params[i] += fmt.Sprintf(" %s.name = ?)", table)
		} else {
			columns[i] = field.Column()
			params[i] = "?"

			if mapping.Type == ReferenceTable || mapping.Type == MapTable {
				columns[i] = strings.Replace(columns[i], "reference", "%s", -1)
			}
		}
	}

	tmpl := stmts[s.kind]
	if replace {
		tmpl = stmts["replace"]
	}

	table := entityTable(s.entity)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		table = "%s_" + table
	}

	sql := fmt.Sprintf(
		tmpl, table,
		strings.Join(columns, ", "), strings.Join(params, ", "))
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("const %s = `%s`", stmtName, sql)
	} else {
		s.register(buf, stmtName, sql)
	}

	return nil
}

func (s *Stmt) id(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Camel(s.entity), s.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}
	sql := naturalKeySelect(s.entity, mapping)
	stmtName := stmtCodeVar(s.entity, "ID")
	s.register(buf, stmtName, sql)

	return nil
}

func (s *Stmt) rename(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Camel(s.entity), s.kind)
	if err != nil {
		return err
	}

	table := entityTable(s.entity)
	where := whereClause(mapping.NaturalKey())

	sql := fmt.Sprintf(stmts[s.kind], table, where)
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	s.register(buf, stmtName, sql)
	return nil
}

func (s *Stmt) update(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityUpdate, ok := s.config["struct"]
	if !ok {
		entityUpdate = entityPut(s.entity)
	}

	mapping, err := Parse(s.packages[s.pkg], entityUpdate, s.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
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

	sql := fmt.Sprintf(
		stmts[s.kind], entityTable(s.entity),
		strings.Join(updates, ", "), "id = ?")
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	s.register(buf, stmtName, sql)

	return nil
}

func (s *Stmt) delete(buf *file.Buffer) error {
	mapping, err := Parse(s.packages[s.pkg], lex.Camel(s.entity), s.kind)
	if err != nil {
		return err
	}

	table := entityTable(s.entity)

	var where string
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		where = "%s_id = ?"
		table = "%s_" + table
	} else {
		where = whereClause(mapping.NaturalKey())
	}

	fields := []*Field{}
	if strings.HasPrefix(s.kind, "delete-by") {
		filters := strings.Split(s.kind[len("delete-by-"):], "-and-")
		for _, filter := range filters {
			field, err := mapping.FilterFieldByName(filter)
			if err != nil {
				return err
			}
			fields = append(fields, field)
		}
		where = whereClause(fields)
	}

	sql := fmt.Sprintf(stmts["delete"], table, where)
	kind := strings.Replace(s.kind, "-", "_", -1)
	stmtName := stmtCodeVar(s.entity, kind)
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("const %s = `%s`", stmtName, sql)
	} else {
		s.register(buf, stmtName, sql)
	}
	return nil
}

// Return a where clause that filters an entity by the given fields
func whereClause(fields []*Field) string {
	via := map[string][]*Field{} // Map scalar fields to their additional indirect fields

	// Filter out indirect fields
	directFields := []*Field{}
	for _, field := range fields {
		if field.IsIndirect() {
			entity := field.Config.Get("via")
			via[entity] = append(via[entity], field)
			continue
		}
		directFields = append(directFields, field)
	}

	where := make([]string, len(directFields))

	for i, field := range directFields {
		if field.IsScalar() {
			ref := lex.Snake(field.Name)
			refTable := entityTable(ref)
			subSelect := fmt.Sprintf("SELECT %s.id FROM %s", refTable, refTable)
			for _, other := range via[ref] {
				otherRef := lex.Snake(other.Name)
				otherTable := entityTable(otherRef)
				subSelect += fmt.Sprintf(" JOIN %s ON %s.id = %s.%s_id", otherTable, otherTable, refTable, otherRef)
			}
			subSelect += fmt.Sprintf(" WHERE")
			for _, other := range via[ref] {
				otherRef := lex.Snake(other.Name)
				otherTable := entityTable(otherRef)
				subSelect += fmt.Sprintf(" %s.name = ? AND", otherTable)
			}
			subSelect += fmt.Sprintf(" %s.name = ?", refTable)
			where[i] = fmt.Sprintf("%s_id = (%s)", ref, subSelect)
		} else {

			where[i] = fmt.Sprintf("%s = ?", field.Column())
		}

	}

	return strings.Join(where, " AND ")
}

// Return a select statement that returns the ID of an entity given its natural key.
func naturalKeySelect(entity string, mapping *Mapping) string {
	nk := mapping.NaturalKey()
	table := entityTable(entity)
	criteria := ""
	for i, field := range nk {
		if i > 0 {
			criteria += " AND "
		}

		var column string
		if field.IsScalar() {
			column = field.Config.Get("join")
		} else {
			column = mapping.FieldColumnName(field.Name, table)
		}

		criteria += fmt.Sprintf("%s = ?", column)
	}

	for _, field := range mapping.ScalarFields() {
		join := field.Config.Get("join")
		right := strings.Split(join, ".")[0]
		via := entityTable(entity)
		if field.Config.Get("via") != "" {
			via = entityTable(field.Config.Get("via"))
		}
		table += fmt.Sprintf(" JOIN %s ON %s.%s_id = %s.id", right, via, lex.Singular(right), right)
	}

	sql := fmt.Sprintf(stmts["id"], entityTable(entity), table, criteria)

	return sql
}

// Output a line of code that registers the given statement and declares the
// associated statement code global variable.
func (s *Stmt) register(buf *file.Buffer, stmtName, sql string, filters ...string) {
	buf.L("var %s = %s.RegisterStmt(`\n%s\n`)", stmtName, s.db, sql)
}

// Map of boilerplate statements.
var stmts = map[string]string{
	"names":   "SELECT %s\n  FROM %s\n  %sORDER BY %s",
	"objects": "SELECT %s\n  FROM %s\n  %sORDER BY %s",
	"create":  "INSERT INTO %s (%s)\n  VALUES (%s)",
	"replace": "INSERT OR REPLACE INTO %s (%s)\n VALUES (%s)",
	"id":      "SELECT %s.id FROM %s\n  WHERE %s",
	"rename":  "UPDATE %s SET name = ? WHERE %s",
	"update":  "UPDATE %s\n  SET %s\n WHERE %s",
	"delete":  "DELETE FROM %s WHERE %s",
}
