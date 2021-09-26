package db

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/lxc/lxd/lxd/db/generate/file"
	"github.com/lxc/lxd/lxd/db/generate/lex"
	"github.com/pkg/errors"
)

// Method generates a code snippet for a particular database query method.
type Method struct {
	db       string                  // Target database (cluster or node)
	pkg      string                  // Package where the entity struct is declared.
	entity   string                  // Name of the database entity
	kind     string                  // Kind of statement to generate
	config   map[string]string       // Configuration parameters
	packages map[string]*ast.Package // Packages to perform for struct declaration lookups
}

// NewMethod return a new method code snippet for executing a certain mapping.
func NewMethod(database, pkg, entity, kind string, config map[string]string) (*Method, error) {
	packages, err := Packages()
	if err != nil {
		return nil, err
	}

	method := &Method{
		db:       database,
		pkg:      pkg,
		entity:   entity,
		kind:     kind,
		config:   config,
		packages: packages,
	}

	return method, nil
}

// Generate the desired method.
func (m *Method) Generate(buf *file.Buffer) error {
	switch operation(m.kind) {
	case "URIs":
		return m.uris(buf)
	case "GetMany":
		return m.getMany(buf)
	case "GetOne":
		return m.getOne(buf)
	case "ID":
		return m.id(buf)
	case "Exists":
		return m.exists(buf)
	case "Create":
		return m.create(buf, false)
	case "CreateOrReplace":
		return m.create(buf, true)
	case "Rename":
		return m.rename(buf)
	case "Update":
		return m.update(buf)
	case "DeleteOne":
		return m.delete(buf, true)
	case "DeleteMany":
		return m.delete(buf, false)
	default:
		return fmt.Errorf("Unknown method kind '%s'", m.kind)
	}
}

// GenerateSignature generates an interface signature for the method.
func (m *Method) GenerateSignature(buf *file.Buffer) error {
	return m.signature(buf, true)
}

func (m *Method) uris(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	filters, ignoredFilters := FiltersFromStmt(m.packages["db"], "names", m.entity, mapping.Filters)

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)
	buf.L("var args []interface{}")
	buf.L("var stmt *sql.Stmt")
	for i, filter := range filters {
		branch := "if"
		if i > 0 {
			branch = "} else if"
		}
		buf.L("%s %s {", branch, activeCriteria(filter, ignoredFilters[i]))

		buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "names", filter...))
		buf.L("args = []interface{}{")

		for _, name := range filter {
			buf.L("filter.%s,", name)
		}

		buf.L("}")
	}

	branch := "if"
	if len(filters) > 0 {
		branch = "} else if"
	}

	buf.L("%s %s {", branch, activeCriteria([]string{}, FieldNames(mapping.Filters)))
	buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "names"))
	buf.L("args = []interface{}{}")
	buf.L("} else {")
	buf.L("return nil, fmt.Errorf(\"No statement exists for the given Filter\")")
	buf.L("}")
	buf.N()

	buf.L("code := %s.EntityTypes[%q]", m.db, m.entity)
	buf.L("formatter := %s.EntityFormatURIs[code]", m.db)
	buf.N()
	buf.L("return query.SelectURIs(stmt, formatter, args...)")

	return nil
}

func (m *Method) getMany(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	// Go type name the objects to return (e.g. api.Foo).
	typ := entityType(m.pkg, m.entity)

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("// Result slice.")
	buf.L("objects := make(%s, 0)", lex.Slice(typ))
	buf.N()

	filters, ignoredFilters := FiltersFromStmt(m.packages["db"], "objects", m.entity, mapping.Filters)
	buf.N()
	buf.L("// Pick the prepared statement and arguments to use based on active criteria.")
	buf.L("var stmt *sql.Stmt")
	buf.L("var args []interface{}")
	buf.N()

	for i, filter := range filters {
		branch := "if"
		if i > 0 {
			branch = "} else if"
		}
		buf.L("%s %s {", branch, activeCriteria(filter, ignoredFilters[i]))

		buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "objects", filter...))
		buf.L("args = []interface{}{")

		for _, name := range filter {
			if name == "Parent" {
				buf.L("len(filter.Parent)+1,")
				buf.L("filter.%s+\"/\",", name)
			} else {
				buf.L("filter.%s,", name)
			}
		}

		buf.L("}")
	}

	branch := "if"
	if len(filters) > 0 {
		branch = "} else if"
	}

	buf.L("%s %s {", branch, activeCriteria([]string{}, FieldNames(mapping.Filters)))
	buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "objects"))
	buf.L("args = []interface{}{}")
	buf.L("} else {")
	buf.L("return nil, fmt.Errorf(\"No statement exists for the given Filter\")")
	buf.L("}")

	buf.N()
	buf.L("// Dest function for scanning a row.")
	buf.L("dest := %s", destFunc("objects", typ, mapping.ColumnFields()))
	buf.N()
	buf.L("// Select.")
	buf.L("err := query.SelectObjects(stmt, dest, args...)")
	m.ifErrNotNil(buf, "nil", fmt.Sprintf("errors.Wrap(err, \"Failed to fetch %s\")", lex.Plural(m.entity)))
	buf.L("return objects, nil")

	return nil
}

func (m *Method) getOne(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("filter := %s{}", entityFilter(m.entity))
	for _, field := range nk {
		buf.L("filter.%s = &%s", field.Name, lex.Minuscule(field.Name))
	}
	buf.N()
	buf.L("objects, err := c.Get%s(filter)", lex.Plural(lex.Camel(m.entity)))
	m.ifErrNotNil(buf, "nil", fmt.Sprintf("errors.Wrap(err, \"Failed to fetch %s\")", lex.Camel(m.entity)))
	buf.L("switch len(objects) {")
	buf.L("case 0:")
	buf.L("        return nil, ErrNoSuchObject")
	buf.L("case 1:")
	buf.L("        return &objects[0], nil")
	buf.L("default:")
	buf.L("        return nil, fmt.Errorf(\"More than one %s matches\")", m.entity)
	buf.L("}")

	return nil
}
func (m *Method) id(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := m.config["struct"]
	if !ok {
		entityCreate = entityPost(m.entity)
	}

	mapping, err := Parse(m.packages[m.pkg], entityCreate, m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "ID"))
	buf.L("rows, err := stmt.Query(%s)", mapping.FieldParams(nk))
	m.ifErrNotNil(buf, "-1", fmt.Sprintf("errors.Wrap(err, \"Failed to get %s ID\")", m.entity))
	buf.L("defer rows.Close()")
	buf.N()
	buf.L("// Ensure we read one and only one row.")
	buf.L("if !rows.Next() {")
	buf.L("        return -1, ErrNoSuchObject")
	buf.L("}")
	buf.L("var id int64")
	buf.L("err = rows.Scan(&id)")
	m.ifErrNotNil(buf, "-1", "errors.Wrap(err, \"Failed to scan ID\")")
	buf.L("if rows.Next() {")
	buf.L("        return -1, fmt.Errorf(\"More than one row returned\")")
	buf.L("}")
	buf.L("err = rows.Err()")
	m.ifErrNotNil(buf, "-1", "errors.Wrap(err, \"Result set failure\")")
	buf.L("return id, nil")

	return nil
}

func (m *Method) exists(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := m.config["struct"]
	if !ok {
		entityCreate = entityPost(m.entity)
	}

	mapping, err := Parse(m.packages[m.pkg], entityCreate, m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("_, err := c.Get%sID(%s)", lex.Camel(m.entity), mapping.FieldParams(nk))
	buf.L("if err != nil {")
	buf.L("        if err == ErrNoSuchObject {")
	buf.L("                return false, nil")
	buf.L("        }")
	buf.L("        return false, err")
	buf.L("}")
	buf.N()
	buf.L("return true, nil")

	return nil
}

func (m *Method) create(buf *file.Buffer, replace bool) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := m.config["struct"]
	if !ok {
		entityCreate = entityPost(m.entity)
	}

	mapping, err := Parse(m.packages[m.pkg], entityCreate, m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	nk := mapping.NaturalKey()
	nkParams := make([]string, len(nk))
	for i, field := range nk {
		nkParams[i] = fmt.Sprintf("object.%s", field.Name)
	}

	kind := "create"
	if replace {
		kind = "create_or_replace"
	} else {
		buf.L("// Check if a %s with the same key exists.", m.entity)
		buf.L("exists, err := c.%sExists(%s)", lex.Camel(m.entity), strings.Join(nkParams, ", "))
		m.ifErrNotNil(buf, "-1", "errors.Wrap(err, \"Failed to check for duplicates\")")
		buf.L("if exists {")
		buf.L("        return -1, fmt.Errorf(\"This %s already exists\")", m.entity)
		buf.L("}")
		buf.N()
	}

	fields := mapping.ColumnFields("ID")
	buf.L("args := make([]interface{}, %d)", len(fields))
	buf.N()

	buf.L("// Populate the statement arguments. ")
	for i, field := range fields {
		buf.L("args[%d] = object.%s", i, field.Name)
	}

	buf.N()

	buf.L("// Prepared statement to use. ")
	buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, kind))
	buf.N()
	buf.L("// Execute the statement. ")
	buf.L("result, err := stmt.Exec(args...)")
	m.ifErrNotNil(buf, "-1", fmt.Sprintf("errors.Wrap(err, \"Failed to create %s\")", m.entity))
	buf.L("id, err := result.LastInsertId()")
	m.ifErrNotNil(buf, "-1", fmt.Sprintf("errors.Wrap(err, \"Failed to fetch %s ID\")", m.entity))

	buf.L("return id, nil")

	return nil
}

func (m *Method) rename(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "rename"))
	buf.L("result, err := stmt.Exec(%s)", "to, "+mapping.FieldParams(nk))
	m.ifErrNotNil(buf, fmt.Sprintf("errors.Wrap(err, \"Rename %s\")", m.entity))
	buf.L("n, err := result.RowsAffected()")
	m.ifErrNotNil(buf, "errors.Wrap(err, \"Fetch affected rows\")")
	buf.L("if n != 1 {")
	buf.L("        return fmt.Errorf(\"Query affected %%d rows instead of 1\", n)")
	buf.L("}")

	buf.L("return nil")

	return nil
}

func (m *Method) update(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	// Support using a different structure or package to pass arguments to Create.
	entityUpdate, ok := m.config["struct"]
	if !ok {
		entityUpdate = entityPut(m.entity)
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	updateMapping, err := Parse(m.packages[m.pkg], entityUpdate, m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}
	fields := updateMapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.

	params := make([]string, len(fields))

	for i, field := range fields {
		params[i] = fmt.Sprintf("object.%s", field.Name)
	}

	//buf.L("id, err := c.Get%s(%s)", lex.Camel(m.entity), FieldArgs(nk))
	buf.L("id, err := c.Get%sID(%s)", lex.Camel(m.entity), mapping.FieldParams(nk))
	m.ifErrNotNil(buf, fmt.Sprintf("errors.Wrap(err, \"Get %s\")", m.entity))
	buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "update"))
	buf.L("result, err := stmt.Exec(%s)", strings.Join(params, ", ")+", id")
	m.ifErrNotNil(buf, fmt.Sprintf("errors.Wrap(err, \"Update %s\")", m.entity))
	buf.L("n, err := result.RowsAffected()")
	m.ifErrNotNil(buf, "errors.Wrap(err, \"Fetch affected rows\")")
	buf.L("if n != 1 {")
	buf.L("        return fmt.Errorf(\"Query updated %%d rows instead of 1\", n)")
	buf.L("}")
	buf.N()

	buf.L("return nil")

	return nil
}

func (m *Method) delete(buf *file.Buffer, deleteOne bool) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	activeFilters := mapping.ActiveFilters(m.kind)
	buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "delete", FieldNames(activeFilters)...))
	buf.L("result, err := stmt.Exec(%s)", mapping.FieldParams(activeFilters))
	m.ifErrNotNil(buf, fmt.Sprintf("errors.Wrap(err, \"Delete %s\")", m.entity))

	if deleteOne {
		buf.L("n, err := result.RowsAffected()")
	} else {
		buf.L("_, err = result.RowsAffected()")
	}

	m.ifErrNotNil(buf, "errors.Wrap(err, \"Fetch affected rows\")")

	if deleteOne {
		buf.L("if n != 1 {")
		buf.L("        return fmt.Errorf(\"Query deleted %%d rows instead of 1\", n)")
		buf.L("}")
	}

	buf.N()
	buf.L("return nil")
	return nil
}

// signature generates a method or interface signature with comments, arguments, and return values.
func (m *Method) signature(buf *file.Buffer, isInterface bool) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}

	if isInterface {
		buf.N()
		buf.L("// %sGenerated is an interface of generated methods for %s", lex.Camel(m.entity), lex.Camel(m.entity))
		buf.L("type %sGenerated interface {", lex.Camel(m.entity))
		defer m.end(buf)
	}

	comment := ""
	args := ""
	rets := ""

	switch operation(m.kind) {
	case "URIs":
		comment = fmt.Sprintf("returns all available %s URIs.", m.entity)
		args = fmt.Sprintf("filter %s", entityFilter(m.entity))
		rets = "([]string, error)"
	case "GetMany":
		comment = fmt.Sprintf("returns all available %s.", lex.Plural(m.entity))
		args = fmt.Sprintf("filter %s", entityFilter(m.entity))
		rets = fmt.Sprintf("(%s, error)", lex.Slice(entityType(m.pkg, m.entity)))
	case "GetOne":
		comment = fmt.Sprintf("returns the %s with the given key.", m.entity)
		args = mapping.FieldArgs(mapping.NaturalKey())
		rets = fmt.Sprintf("(%s, error)", lex.Star(entityType(m.pkg, m.entity)))
	case "ID":
		comment = fmt.Sprintf("return the ID of the %s with the given key.", m.entity)
		args = mapping.FieldArgs(mapping.NaturalKey())
		rets = "(int64, error)"
	case "Exists":
		comment = fmt.Sprintf("checks if a %s with the given key exists.", m.entity)
		args = mapping.FieldArgs(mapping.NaturalKey())
		rets = "(bool, error)"
	case "Create":
		entityCreate, ok := m.config["struct"]
		if !ok {
			entityCreate = entityPost(m.entity)
		}
		comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
		args = fmt.Sprintf("object %s", entityType(m.pkg, entityCreate))
		rets = "(int64, error)"
	case "CreateOrReplace":
		entityCreate, ok := m.config["struct"]
		if !ok {
			entityCreate = entityPost(m.entity)
		}
		comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
		args = fmt.Sprintf("object %s", entityType(m.pkg, entityCreate))
		rets = "(int64, error)"
	case "Rename":
		comment = fmt.Sprintf("renames the %s matching the given key parameters.", m.entity)
		args = mapping.FieldArgs(mapping.NaturalKey(), "to string")
		rets = "error"
	case "Update":
		entityUpdate, ok := m.config["struct"]
		if !ok {
			entityUpdate = entityPut(m.entity)
		}
		comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
		args = mapping.FieldArgs(mapping.NaturalKey(), fmt.Sprintf("object %s", entityType(m.pkg, entityUpdate)))
		rets = "error"
	case "DeleteOne":
		comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
		args = mapping.FieldArgs(mapping.ActiveFilters(m.kind))
		rets = "error"
	case "DeleteMany":
		comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
		args = mapping.FieldArgs(mapping.ActiveFilters(m.kind))
		rets = "error"
	default:
		return fmt.Errorf("Unknown method kind '%s'", m.kind)
	}

	m.begin(buf, comment, args, rets, isInterface)
	return nil
}

func (m *Method) begin(buf *file.Buffer, comment string, args string, rets string, isInterface bool) {
	name := ""
	entity := lex.Camel(m.entity)
	switch operation(m.kind) {
	case "URIs":
		name = fmt.Sprintf("Get%sURIs", entity)
	case "GetMany":
		name = fmt.Sprintf("Get%s", lex.Plural(entity))
	case "GetOne":
		name = fmt.Sprintf("Get%s", entity)
	case "ID":
		name = fmt.Sprintf("Get%sID", entity)
	case "Exists":
		name = fmt.Sprintf("%sExists", entity)
	case "Create":
		name = fmt.Sprintf("Create%s", entity)
	case "CreateOrReplace":
		name = fmt.Sprintf("CreateOrReplace%s", entity)
	case "Rename":
		name = fmt.Sprintf("Rename%s", entity)
	case "Update":
		name = fmt.Sprintf("Update%s", entity)
	case "DeleteOne":
		name = fmt.Sprintf("Delete%s", entity)
	case "DeleteMany":
		name = fmt.Sprintf("Delete%ss", entity)
	default:
		name = fmt.Sprintf("%s%s", entity, m.kind)
	}
	receiver := fmt.Sprintf("c %s", dbTxType(m.db))

	buf.L("// %s %s", name, comment)
	buf.L("// generator: %s %s", m.entity, m.kind)

	if isInterface {
		buf.L("%s(%s) %s", name, args, rets)
	} else {
		buf.L("func (%s) %s(%s) %s {", receiver, name, args, rets)
	}
}

func (m *Method) ifErrNotNil(buf *file.Buffer, rets ...string) {
	buf.L("if err != nil {")
	buf.L("return %s", strings.Join(rets, ", "))
	buf.L("}")
	buf.N()
}

func (m *Method) end(buf *file.Buffer) {
	buf.L("}")
}
