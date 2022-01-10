package db

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/lxc/lxd/lxd/db/generate/file"
	"github.com/lxc/lxd/lxd/db/generate/lex"
)

// Method generates a code snippet for a particular database query method.
type Method struct {
	db       string                  // Target database (cluster or node)
	pkg      string                  // Package where the entity struct is declared.
	entity   string                  // Name of the database entity
	kind     string                  // Kind of statement to generate
	ref      string                  // ref is the current reference method for the method kind
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
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Unable to parse go struct %q: %w", lex.Camel(m.entity), err)
	}
	if mapping.Type != EntityTable {
		switch operation(m.kind) {
		case "GetMany":
			return m.getMany(buf)
		case "Create":
			return m.create(buf, false)
		case "Update":
			return m.update(buf)
		case "DeleteMany":
			return m.delete(buf, false)
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)

		}
	}

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
	buf.N()
	buf.L("// %sGenerated is an interface of generated methods for %s", lex.Camel(m.entity), lex.Camel(m.entity))
	buf.L("type %sGenerated interface {", lex.Camel(m.entity))
	defer m.end(buf)
	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			m.ref = fieldName
			err := m.signature(buf, true)
			if err != nil {
				return err
			}

			m.ref = ""
			buf.N()
		}
	}

	return m.signature(buf, true)
}

func (m *Method) uris(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	// Go type name the objects to return (e.g. api.Foo).
	typ := entityType(m.pkg, m.entity)

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("var err error")
	buf.N()
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
	buf.L("err = query.SelectObjects(stmt, dest, args...)")
	m.ifErrNotNil(buf, "nil", fmt.Sprintf("fmt.Errorf(\"Failed to fetch from \\\"%s\\\" table: %%w\", err)", entityTable(m.entity)))
	buf.L("uris := make([]string, len(objects))")
	buf.L("for i := range objects {")
	name := mapping.Identifier().Name
	buf.L("uri := api.NewURL().Path(version.APIVersion, \"%s\", objects[i].%s)", lex.Plural(m.entity), name)
	for _, field := range mapping.NaturalKey() {
		if field.Name != name {
			buf.L("uri.%s(objects[i].%s)", field.Name, field.Name)
		}
	}
	buf.N()
	buf.L("uris[i] = uri.String()")
	buf.L("}")
	buf.N()
	buf.L("return uris, nil")

	return nil
}

func (m *Method) getMany(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			refMapping, err := Parse(m.packages[m.pkg], fieldName, m.kind)
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			defer m.getRefs(buf, refMapping)
		}
	}

	// Go type name the objects to return (e.g. api.Foo).
	typ := entityType(m.pkg, m.entity)

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("var err error")
	buf.N()
	buf.L("// Result slice.")
	buf.L("objects := make(%s, 0)", lex.Slice(typ))
	buf.N()
	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		stmtVar := stmtCodeVar(m.entity, "objects")
		stmtLocal := stmtVar + "Local"
		buf.L("%s := strings.Replace(%s, \"%%s_id\", fmt.Sprintf(\"%%s_id\", parent), -1)", stmtLocal, stmtVar)
		buf.L("fillParent := make([]interface{}, strings.Count(%s, \"%%s\"))", stmtLocal)
		buf.L("for i := range fillParent {")
		buf.L("fillParent[i] = strings.Replace(parent, \"_\", \"s_\", -1) + \"s\"")
		buf.L("}")
		buf.N()
		buf.L("stmt, err := c.prepare(fmt.Sprintf(%s, fillParent...))", stmtLocal)
		m.ifErrNotNil(buf, "nil", "err")
		buf.L("args := []interface{}{}")
	} else if mapping.Type == AssociationTable {
		filter := m.config["struct"] + "ID"
		buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "objects", filter))
		buf.L("args := []interface{}{%s.ID}", lex.Minuscule(m.config["struct"]))
	} else {
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
	}
	buf.N()
	buf.L("// Dest function for scanning a row.")
	buf.L("dest := %s", destFunc("objects", typ, mapping.ColumnFields()))
	buf.N()
	buf.L("// Select.")
	buf.L("err = query.SelectObjects(stmt, dest, args...)")
	m.ifErrNotNil(buf, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", err)`, entityTable(m.entity)))

	for _, field := range mapping.RefFields() {
		// TODO: Eliminate UsedBy fields and replace with dedicated slices for entities.
		if field.Name == "UsedBy" {
			buf.L("// Use non-generated custom method for UsedBy fields.")
			buf.L("for i := range objects {")
			buf.L("usedBy, err := c.Get%sUsedBy(objects[i])", lex.Camel(m.entity))
			m.ifErrNotNil(buf, "nil", "err")
			buf.L("objects[i].UsedBy = usedBy")
			buf.L("}")
			buf.N()
			continue
		}

		refStruct := lex.Singular(field.Name)
		refVar := lex.Minuscule(refStruct)
		refSlice := lex.Plural(refVar)
		refMapping, err := Parse(m.packages[m.pkg], refStruct, "")
		if err != nil {
			return fmt.Errorf("Could not find definition for reference struct %q in package %q: %w", refStruct, m.db, err)
		}

		switch refMapping.Type {
		case EntityTable:
			assocStruct := mapping.Name + field.Name
			buf.L("%s, err := c.Get%s()", lex.Minuscule(assocStruct), assocStruct)
			m.ifErrNotNil(buf, "nil", "err")
			buf.L("for i := range objects {")
			buf.L("objects[i].%s = make([]string, 0)", field.Name)
			buf.L("if refIDs, ok := %s[objects[i].ID]; ok {", lex.Minuscule(assocStruct))
			buf.L("for _, refID := range refIDs {")
			buf.L("%sURIs, err := c.Get%sURIs(%sFilter{ID: &refID})", refVar, refStruct, refStruct)
			m.ifErrNotNil(buf, "nil", "err")
			if field.Config.Get("uri") == "" {
				uriName := strings.ReplaceAll(lex.Snake(refSlice), "_", "-")
				buf.L("uris, err := urlsToResourceNames(\"/%s\", %sURIs...)", uriName, refVar)
				m.ifErrNotNil(buf, "nil", "err")
				buf.L("%sURIs = uris", refVar)
			}
			buf.L("objects[i].%s = append(objects[i].%s, %sURIs...)", field.Name, field.Name, refVar)
			buf.L("}")
			buf.L("}")
			buf.L("}")
		case ReferenceTable:
			if mapping.Type == ReferenceTable {
				// A reference table should let its child reference know about its parent.
				buf.L("%s, err := c.Get%s(parent+\"_%s\")", refSlice, lex.Plural(refStruct), m.entity)
				m.ifErrNotNil(buf, "nil", "err")
			} else {
				buf.L("%s, err := c.Get%s(\"%s\")", refSlice, lex.Plural(refStruct), m.entity)
				m.ifErrNotNil(buf, "nil", "err")
			}
			buf.L("for i := range objects {")
			if field.Type.Code == TypeSlice {
				buf.L("objects[i].%s = %s[objects[i].ID]", lex.Plural(refStruct), refSlice)
			} else if field.Type.Code == TypeMap {
				buf.L("objects[i].%s = map[string]%s{}", lex.Plural(refStruct), refStruct)
				buf.L("for _, obj := range %s[objects[i].ID] {", refSlice)
				buf.L("if _, ok := objects[i].%s[obj.%s]; !ok {", lex.Plural(refStruct), refMapping.NaturalKey()[0].Name)
				buf.L("objects[i].%s[obj.%s] = obj", lex.Plural(refStruct), refMapping.NaturalKey()[0].Name)
				buf.L("} else {")
				buf.L("return nil, fmt.Errorf(\"Found duplicate %s with name %%q\", obj.%s)", refStruct, refMapping.NaturalKey()[0].Name)
				buf.L("}")
				buf.L("}")
			}
			buf.L("}")
		case MapTable:
			if mapping.Type == ReferenceTable {
				// A reference table should let its child reference know about its parent.
				buf.L("%s, err := c.Get%s(parent+\"_%s\")", refSlice, lex.Plural(refStruct), m.entity)
				m.ifErrNotNil(buf, "nil", "err")
			} else {
				buf.L("%s, err := c.Get%s(\"%s\")", refSlice, lex.Plural(refStruct), m.entity)
				m.ifErrNotNil(buf, "nil", "err")
			}
			buf.L("for i := range objects {")
			buf.L("if _, ok := %s[objects[i].ID]; !ok {", refSlice)
			buf.L("objects[i].%s = map[string]string{}", refStruct)
			buf.L("} else {")
			buf.L("objects[i].%s = %s[objects[i].ID]", lex.Plural(refStruct), refSlice)
			buf.L("}")
			buf.L("}")
		}

		buf.N()
	}

	switch mapping.Type {
	case AssociationTable:
		ref := strings.Replace(mapping.Name, m.config["struct"], "", -1)
		buf.L("result := make([]%s, len(objects))", ref)
		buf.L("for i, object := range objects {")
		buf.L("%s, err := c.Get%s(%sFilter{ID: &object.%sID})", lex.Minuscule(ref), lex.Plural(ref), ref, ref)
		m.ifErrNotNil(buf, "nil", "err")
		buf.L("result[i] = %s[0]", lex.Minuscule(ref))
		buf.L("}")
		buf.N()
		buf.L("return result, nil")
	case ReferenceTable:
		buf.L("resultMap := map[int][]%s{}", mapping.Name)
		buf.L("for _, object := range objects {")
		buf.L("if _, ok := resultMap[object.ReferenceID]; !ok {")
		buf.L("resultMap[object.ReferenceID] = []%s{}", mapping.Name)
		buf.L("}")
		buf.L("resultMap[object.ReferenceID] = append(resultMap[object.ReferenceID], object)")
		buf.L("}")
		buf.N()
		buf.L("return resultMap, nil")
	case MapTable:
		buf.L("resultMap := map[int]map[string]string{}")
		buf.L("for _, object := range objects {")
		buf.L("if _, ok := resultMap[object.ReferenceID]; !ok {")
		buf.L("resultMap[object.ReferenceID] = map[string]string{}")
		buf.L("}")
		buf.L("resultMap[object.ReferenceID][object.Key] = object.Value")
		buf.L("}")
		buf.N()
		buf.L("return resultMap, nil")
	case EntityTable:
		buf.L("return objects, nil")
	}

	return nil
}

func (m *Method) getRefs(buf *file.Buffer, refMapping *Mapping) error {
	m.ref = refMapping.Name
	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	// reset m.ref in case m.signature is called again.
	m.ref = ""

	refStruct := refMapping.Name
	refVar := lex.Minuscule(refStruct)
	refList := lex.Plural(refVar)
	refParent := lex.Minuscule(lex.Camel(m.entity))
	refParentList := refParent + lex.Camel(refList)

	switch refMapping.Type {
	case ReferenceTable:
		buf.L("%s, err := c.Get%s(\"%s\")", refParentList, lex.Plural(refStruct), m.entity)
		m.ifErrNotNil(buf, "nil", "err")
		buf.L("%s := map[string]%s{}", refList, refStruct)
		buf.L("for _, ref := range %s[%sID] {", refParentList, refParent)
		buf.L("if _, ok := %s[ref.%s]; !ok {", refList, refMapping.Identifier().Name)
		buf.L("%s[ref.%s] = ref", refList, refMapping.Identifier().Name)
		buf.L("} else {")
		buf.L("return nil, fmt.Errorf(\"Found duplicate %s with name %%q\", ref.%s)", refStruct, refMapping.Identifier().Name)
		buf.L("}")
		buf.L("}")
	case MapTable:
		buf.L("%s, err := c.Get%s(\"%s\")", refParentList, lex.Plural(refStruct), m.entity)
		m.ifErrNotNil(buf, "nil", "err")
		buf.L("%s, ok := %s[%sID]", refList, refParentList, refParent)
		buf.L("if !ok {")
		buf.L("%s = map[string]string{}", refList)
		buf.L("}")
	}

	buf.L("return %s, nil", refList)

	return nil
}

func (m *Method) getOne(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
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
	m.ifErrNotNil(buf, "nil", fmt.Sprintf(`fmt.Errorf("Failed to fetch from \"%s\" table: %%w", err)`, entityTable(m.entity)))
	buf.L("switch len(objects) {")
	buf.L("case 0:")
	buf.L("        return nil, ErrNoSuchObject")
	buf.L("case 1:")
	buf.L("        return &objects[0], nil")
	buf.L("default:")
	buf.L(`        return nil, fmt.Errorf("More than one \"%s\" entry matches")`, entityTable(m.entity))
	buf.L("}")

	return nil
}

func (m *Method) id(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := m.config["struct"]
	if !ok {
		entityCreate = lex.Camel(m.entity)
	}

	mapping, err := Parse(m.packages[m.pkg], entityCreate, m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "ID"))
	buf.L("rows, err := stmt.Query(%s)", mapping.FieldParams(nk))
	m.ifErrNotNil(buf, "-1", fmt.Sprintf(`fmt.Errorf("Failed to get \"%s\" ID: %%w", err)`, entityTable(m.entity)))
	buf.L("defer rows.Close()")
	buf.N()
	buf.L("// Ensure we read one and only one row.")
	buf.L("if !rows.Next() {")
	buf.L("        return -1, ErrNoSuchObject")
	buf.L("}")
	buf.L("var id int64")
	buf.L("err = rows.Scan(&id)")
	m.ifErrNotNil(buf, "-1", "fmt.Errorf(\"Failed to scan ID: %w\", err)")
	buf.L("if rows.Next() {")
	buf.L("        return -1, fmt.Errorf(\"More than one row returned\")")
	buf.L("}")
	buf.L("err = rows.Err()")
	m.ifErrNotNil(buf, "-1", "fmt.Errorf(\"Result set failure: %w\", err)")
	buf.L("return id, nil")

	return nil
}

func (m *Method) exists(buf *file.Buffer) error {
	// Support using a different structure or package to pass arguments to Create.
	entityCreate, ok := m.config["struct"]
	if !ok {
		entityCreate = lex.Camel(m.entity)
	}

	mapping, err := Parse(m.packages[m.pkg], entityCreate, m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
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
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			refMapping, err := Parse(m.packages[m.pkg], fieldName, m.kind)
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			defer m.createRefs(buf, refMapping)
		}
	}

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	if mapping.Type == MapTable {
		buf.L("// An empty value means we are unsetting this key, so just return.")
		buf.L("if object.Value == \"\" {")
		buf.L("return nil")
		buf.L("}")
		buf.N()
	}

	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		stmtVar := stmtCodeVar(m.entity, "create")
		stmtLocal := stmtVar + "Local"
		buf.L("%s := strings.Replace(%s, \"%%s_id\", fmt.Sprintf(\"%%s_id\", parent), -1)", stmtLocal, stmtVar)
		buf.L("fillParent := make([]interface{}, strings.Count(%s, \"%%s\"))", stmtLocal)
		buf.L("for i := range fillParent {")
		buf.L("fillParent[i] = strings.Replace(parent, \"_\", \"s_\", -1) + \"s\"")
		buf.L("}")
		buf.N()
		buf.L("stmt, err := c.prepare(fmt.Sprintf(%s, fillParent...))", stmtLocal)
		m.ifErrNotNil(buf, "err")
		createParams := ""
		columnFields := mapping.ColumnFields("ID")
		for i, field := range columnFields {
			createParams += fmt.Sprintf("object.%s", field.Name)
			if i < len(columnFields) {
				createParams += ", "
			}
		}

		refFields := mapping.RefFields()
		if len(refFields) == 0 {
			buf.L("_, err = stmt.Exec(%s)", createParams)
			m.ifErrNotNil(buf, fmt.Sprintf(`fmt.Errorf("Insert failed for \"%%s_%s\" table: %%w", parent, err)`, lex.Plural(m.entity)))
		} else {
			buf.L("result, err := stmt.Exec(%s)", createParams)
			m.ifErrNotNil(buf, fmt.Sprintf(`fmt.Errorf("Insert failed for \"%%s_%s\" table: %%w", parent, err)`, lex.Plural(m.entity)))
			buf.L("id, err := result.LastInsertId()")
			m.ifErrNotNil(buf, "fmt.Errorf(\"Failed to fetch ID: %w\", err)")
		}
	} else {
		nk := mapping.NaturalKey()
		nkParams := make([]string, len(nk))
		for i, field := range nk {
			nkParams[i] = fmt.Sprintf("object.%s", field.Name)
		}

		kind := "create"
		if mapping.Type != AssociationTable {
			if replace {
				kind = "create_or_replace"
			} else {
				buf.L("// Check if a %s with the same key exists.", m.entity)
				buf.L("exists, err := c.%sExists(%s)", lex.Camel(m.entity), strings.Join(nkParams, ", "))
				m.ifErrNotNil(buf, "-1", "fmt.Errorf(\"Failed to check for duplicates: %w\", err)")
				buf.L("if exists {")
				buf.L(`        return -1, fmt.Errorf("This \"%s\" entry already exists")`, entityTable(m.entity))
				buf.L("}")
				buf.N()
			}
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
		m.ifErrNotNil(buf, "-1", fmt.Sprintf(`fmt.Errorf("Failed to create \"%s\" entry: %%w", err)`, entityTable(m.entity)))
		buf.L("id, err := result.LastInsertId()")
		m.ifErrNotNil(buf, "-1", fmt.Sprintf(`fmt.Errorf("Failed to fetch \"%s\" entry ID: %%w", err)`, entityTable(m.entity)))
	}

	for _, field := range mapping.RefFields() {
		if field.Name == "UsedBy" {
			continue
		}

		refStruct := lex.Singular(field.Name)
		refMapping, err := Parse(m.packages[m.pkg], lex.Singular(field.Name), "")
		if err != nil {
			return fmt.Errorf("Parse entity struct: %w", err)
		}

		switch refMapping.Type {
		case EntityTable:
			assocStruct := mapping.Name + refStruct
			buf.L("// Update association table.")
			buf.L("object.ID = int(id)")
			buf.L("err = c.Update%s(object)", lex.Plural(assocStruct))
			m.ifErrNotNil(buf, "-1", fmt.Sprintf("fmt.Errorf(\"Could not update association table: %%w\", err)"))
			continue
		case ReferenceTable:
			buf.L("for _, insert := range object.%s {", field.Name)
			buf.L("insert.ReferenceID = int(id)")
		case MapTable:
			buf.L("referenceID := int(id)")
			buf.L("for key, value := range object.%s {", field.Name)
			buf.L("insert := %s{", field.Name)
			for _, ref := range refMapping.ColumnFields("ID") {
				buf.L("%s: %s,", ref.Name, lex.Minuscule(ref.Name))
			}
			buf.L("}")
			buf.N()
		}

		if mapping.Type != EntityTable {
			buf.L("err = c.Create%s(parent + \"_%s\", insert)", refStruct, m.entity)
			m.ifErrNotNil(buf, fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", field.Name, mapping.Name))
		} else {
			buf.L("err = c.Create%s(\"%s\", insert)", refStruct, m.entity)
			m.ifErrNotNil(buf, "-1", fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", field.Name, mapping.Name))
		}
		buf.L("}")
	}

	if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		buf.L("return nil")
	} else {
		buf.L("return id, nil")
	}
	return nil
}

func (m *Method) createRefs(buf *file.Buffer, refMapping *Mapping) error {
	m.ref = refMapping.Name
	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	// reset m.ref in case m.signature is called again.
	m.ref = ""

	refStruct := refMapping.Name
	refVar := lex.Minuscule(refStruct)
	refParent := lex.Minuscule(lex.Camel(m.entity))

	switch refMapping.Type {
	case ReferenceTable:
		buf.L("%s.ReferenceID = int(%sID)", refVar, refParent)
		buf.L("err := c.Create%s(\"%s\", %s)", refStruct, m.entity, refVar)
		m.ifErrNotNil(buf, fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", refStruct, lex.Camel(m.entity)))
	case MapTable:
		buf.L("referenceID := int(%sID)", refParent)
		buf.L("for key, value := range %s {", refVar)
		buf.L("insert := %s{", refStruct)
		for _, ref := range refMapping.ColumnFields("ID") {
			buf.L("%s: %s,", ref.Name, lex.Minuscule(ref.Name))
		}
		buf.L("}")
		buf.N()
		buf.L("err := c.Create%s(\"%s\", insert)", refStruct, m.entity)
		m.ifErrNotNil(buf, fmt.Sprintf("fmt.Errorf(\"Insert %s failed for %s: %%w\", err)", refStruct, lex.Camel(m.entity)))
		buf.L("}")
	}

	buf.L("return nil")

	return nil
}

func (m *Method) rename(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "rename"))
	buf.L("result, err := stmt.Exec(%s)", "to, "+mapping.FieldParams(nk))
	m.ifErrNotNil(buf, fmt.Sprintf("fmt.Errorf(\"Rename %s failed: %%w\", err)", mapping.Name))
	buf.L("n, err := result.RowsAffected()")
	m.ifErrNotNil(buf, "fmt.Errorf(\"Fetch affected rows failed: %w\", err)")
	buf.L("if n != 1 {")
	buf.L("        return fmt.Errorf(\"Query affected %%d rows instead of 1\", n)")
	buf.L("}")

	buf.L("return nil")

	return nil
}

func (m *Method) update(buf *file.Buffer) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	// Support using a different structure or package to pass arguments to Create.
	entityUpdate, ok := m.config["struct"]
	if !ok {
		entityUpdate = mapping.Name
	}

	if m.config["references"] != "" {
		refFields := strings.Split(m.config["references"], ",")
		for _, fieldName := range refFields {
			refMapping, err := Parse(m.packages[m.pkg], fieldName, m.kind)
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			defer m.updateRefs(buf, refMapping)
		}
	}

	nk := mapping.NaturalKey()

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	switch mapping.Type {
	case AssociationTable:
		ref := strings.Replace(mapping.Name, m.config["struct"], "", -1)
		refMapping, err := Parse(m.packages[m.pkg], ref, "")
		if err != nil {
			return fmt.Errorf("Parse entity struct: %w", err)
		}

		buf.L("// Delete current entry.")
		buf.L("err := c.Delete%s%s(%s)", m.config["struct"], lex.Plural(ref), lex.Minuscule(m.config["struct"]))
		m.ifErrNotNil(buf, "err")
		buf.L("// Insert new entries.")
		buf.L("for _, entry := range %s {", lex.Plural(lex.Minuscule(ref)))
		buf.L("refID, err := c.Get%sID(entry.%s)", ref, refMapping.Identifier().Name)
		m.ifErrNotNil(buf, "err")
		fields := fmt.Sprintf("%sID: %s.ID, %sID: int(refID)", m.config["struct"], lex.Minuscule(m.config["struct"]), ref)
		buf.L("%s := %s{%s}", lex.Minuscule(mapping.Name), mapping.Name, fields)
		buf.L("_, err = c.Create%s%s(%s)", m.config["struct"], ref, lex.Minuscule(mapping.Name))
		m.ifErrNotNil(buf, "err")
		buf.L("return nil")
		buf.L("}")
	case ReferenceTable:
		buf.L("// Delete current entry.")
		buf.L("err := c.Delete%s(parent, referenceID)", lex.Camel(lex.Plural(m.entity)))
		m.ifErrNotNil(buf, "err")
		buf.L("// Insert new entries.")
		buf.L("for _, object := range %s {", lex.Plural(m.entity))
		buf.L("object.ReferenceID = referenceID")
		buf.L("err = c.Create%s(parent, object)", lex.Camel(m.entity))
		buf.L("}")
		m.ifErrNotNil(buf, "err")
	case MapTable:
		buf.L("// Delete current entry.")
		buf.L("err := c.Delete%s(parent, referenceID)", lex.Camel(lex.Plural(m.entity)))
		m.ifErrNotNil(buf, "err")
		buf.L("// Insert new entries.")
		buf.L("for key, value := range config {")
		buf.L("object := %s{", mapping.Name)
		for _, field := range mapping.ColumnFields("ID") {
			buf.L("%s: %s,", field.Name, lex.Minuscule(field.Name))
		}
		buf.L("}")
		buf.N()
		buf.L("err = c.Create%s(parent, object)", lex.Camel(m.entity))
		buf.L("}")
		m.ifErrNotNil(buf, "err")
	case EntityTable:
		updateMapping, err := Parse(m.packages[m.pkg], entityUpdate, m.kind)
		if err != nil {
			return fmt.Errorf("Parse entity struct: %w", err)
		}
		fields := updateMapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.

		params := make([]string, len(fields))

		for i, field := range fields {
			params[i] = fmt.Sprintf("object.%s", field.Name)
		}

		buf.L("id, err := c.Get%sID(%s)", lex.Camel(m.entity), mapping.FieldParams(nk))
		m.ifErrNotNil(buf, "err")
		buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "update"))
		buf.L("result, err := stmt.Exec(%s)", strings.Join(params, ", ")+", id")
		m.ifErrNotNil(buf, fmt.Sprintf(`fmt.Errorf("Update \"%s\" entry failed: %%w", err)`, entityTable(m.entity)))
		buf.L("n, err := result.RowsAffected()")
		m.ifErrNotNil(buf, "fmt.Errorf(\"Fetch affected rows: %w\", err)")
		buf.L("if n != 1 {")
		buf.L("        return fmt.Errorf(\"Query updated %%d rows instead of 1\", n)")
		buf.L("}")
		buf.N()

		for _, field := range mapping.RefFields() {
			// TODO: Eliminate UsedBy fields and move to dedicated slices for entities.
			if field.Name == "UsedBy" {
				continue
			}

			refStruct := lex.Singular(field.Name)
			refMapping, err := Parse(m.packages[m.pkg], lex.Singular(field.Name), "")
			if err != nil {
				return fmt.Errorf("Parse entity struct: %w", err)
			}

			switch refMapping.Type {
			case EntityTable:
				assocStruct := mapping.Name + refStruct
				buf.L("// Update association table.")
				buf.L("object.ID = int(id)")
				buf.L("err = c.Update%s(object)", lex.Plural(assocStruct))
				m.ifErrNotNil(buf, "fmt.Errorf(\"Could not update association table: %w\", err)")
			case ReferenceTable:
				buf.L("err = c.Update%s(\"%s\", int(id), object.%s)", lex.Singular(field.Name), m.entity, field.Name)
				m.ifErrNotNil(buf, fmt.Sprintf("fmt.Errorf(\"Replace %s for %s failed: %%w\", err)", field.Name, mapping.Name))
			case MapTable:
				buf.L("err = c.Update%s(\"%s\", int(id), object.%s)", lex.Singular(field.Name), m.entity, field.Name)
				m.ifErrNotNil(buf, fmt.Sprintf("fmt.Errorf(\"Replace %s for %s failed: %%w\", err)", field.Name, mapping.Name))
				buf.N()
			}

		}
	}

	buf.L("return nil")

	return nil
}

func (m *Method) updateRefs(buf *file.Buffer, refMapping *Mapping) error {
	m.ref = refMapping.Name
	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)

	// reset m.ref in case m.signature is called again.
	m.ref = ""

	refStruct := refMapping.Name
	refVar := lex.Minuscule(refStruct)
	refList := lex.Plural(refVar)
	refParent := lex.Minuscule(lex.Camel(m.entity))

	buf.L("err := c.Update%s(\"%s\", int(%sID), %s)", lex.Plural(refStruct), m.entity, refParent, refList)
	m.ifErrNotNil(buf, fmt.Sprintf("fmt.Errorf(\"Replace %s for %s failed: %%w\", err)", refStruct, lex.Camel(m.entity)))
	buf.L("return nil")

	return nil
}

func (m *Method) delete(buf *file.Buffer, deleteOne bool) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	if err := m.signature(buf, false); err != nil {
		return err
	}

	defer m.end(buf)
	if mapping.Type == AssociationTable {
		buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "delete", m.config["struct"]+"ID"))
		buf.L("result, err := stmt.Exec(int(object.ID))")
		m.ifErrNotNil(buf, fmt.Sprintf(`fmt.Errorf("Delete \"%s\" entry failed: %%w", err)`, entityTable(m.entity)))
	} else if mapping.Type == ReferenceTable || mapping.Type == MapTable {
		stmtVar := stmtCodeVar(m.entity, "delete")
		stmtLocal := stmtVar + "Local"
		buf.L("%s := strings.Replace(%s, \"%%s_id\", fmt.Sprintf(\"%%s_id\", parent), -1)", stmtLocal, stmtVar)
		buf.L("fillParent := make([]interface{}, strings.Count(%s, \"%%s\"))", stmtLocal)
		buf.L("for i := range fillParent {")
		buf.L("fillParent[i] = strings.Replace(parent, \"_\", \"s_\", -1) + \"s\"")
		buf.L("}")
		buf.N()
		buf.L("stmt, err := c.prepare(fmt.Sprintf(%s, fillParent...))", stmtLocal)
		m.ifErrNotNil(buf, "err")
		buf.L("result, err := stmt.Exec(referenceID)")
		m.ifErrNotNil(buf, fmt.Sprintf(`fmt.Errorf("Delete entry for \"%%s_%s\" failed: %%w", parent, err)`, m.entity))
	} else {
		activeFilters := mapping.ActiveFilters(m.kind)
		buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "delete", FieldNames(activeFilters)...))
		buf.L("result, err := stmt.Exec(%s)", mapping.FieldParams(activeFilters))
		m.ifErrNotNil(buf, fmt.Sprintf(`fmt.Errorf("Delete \"%s\": %%w", err)`, entityTable(m.entity)))
	}

	if deleteOne {
		buf.L("n, err := result.RowsAffected()")
	} else {
		buf.L("_, err = result.RowsAffected()")
	}

	m.ifErrNotNil(buf, "fmt.Errorf(\"Fetch affected rows: %w\", err)")
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
		return fmt.Errorf("Parse entity struct: %w", err)
	}

	comment := ""
	args := ""
	rets := ""

	switch mapping.Type {
	case AssociationTable:
		switch operation(m.kind) {
		case "GetMany":
			ref := strings.Replace(mapping.Name, m.config["struct"], "", -1)
			comment = fmt.Sprintf("returns all available %s.", lex.Plural(m.entity))
			args = fmt.Sprintf("%s %s", lex.Minuscule(m.config["struct"]), m.config["struct"])
			rets = fmt.Sprintf("([]%s, error)", ref)
		case "Create":
			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args = fmt.Sprintf("object %s", mapping.Name)
			rets = "(int64, error)"
		case "Update":
			ref := strings.Replace(mapping.Name, m.config["struct"], "", -1)
			comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
			args = fmt.Sprintf("%s %s, %s []%s", lex.Minuscule(m.config["struct"]), m.config["struct"], lex.Plural(lex.Minuscule(ref)), ref)
			rets = "error"
		case "DeleteMany":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args = fmt.Sprintf("object %s", m.config["struct"])
			rets = "error"
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}
	case ReferenceTable:
		switch operation(m.kind) {
		case "GetMany":
			comment = fmt.Sprintf("returns all available %s for the parent entity.", lex.Plural(m.entity))
			args = "parent string"
			rets = fmt.Sprintf("(map[int][]%s, error)", mapping.Name)
		case "Create":
			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args = fmt.Sprintf("parent string, object %s", mapping.Name)
			rets = "error"
		case "Update":
			comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
			args = fmt.Sprintf("parent string, referenceID int, %s map[string]%s", lex.Plural(m.entity), mapping.Name)
			rets = "error"
		case "DeleteMany":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args = "parent string, referenceID int"
			rets = "error"
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}
	case MapTable:
		switch operation(m.kind) {
		case "GetMany":
			comment = fmt.Sprintf("returns all available %s.", lex.Plural(m.entity))
			args = "parent string"
			rets = "(map[int]map[string]string, error)"
		case "Create":
			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args = fmt.Sprintf("parent string, object %s", mapping.Name)
			rets = "error"
		case "Update":
			comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
			args = "parent string, referenceID int, config map[string]string"
			rets = "error"
		case "DeleteMany":
			comment = fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
			args = "parent string, referenceID int"
			rets = "error"
		default:
			return fmt.Errorf("Unknown method kind '%s'", m.kind)
		}
	case EntityTable:
		switch operation(m.kind) {
		case "URIs":
			comment = fmt.Sprintf("returns all available %s URIs.", m.entity)
			args = fmt.Sprintf("filter %s", entityFilter(m.entity))
			rets = "([]string, error)"
		case "GetMany":
			if m.ref == "" {
				comment = fmt.Sprintf("returns all available %s.", lex.Plural(m.entity))
				args = fmt.Sprintf("filter %s", entityFilter(m.entity))
				rets = fmt.Sprintf("(%s, error)", lex.Slice(entityType(m.pkg, m.entity)))
			} else {
				comment = fmt.Sprintf("returns all available %s %s", mapping.Name, lex.Plural(m.ref))
				args = fmt.Sprintf("%sID int", lex.Minuscule(mapping.Name))
				refMapping, err := Parse(m.packages[m.pkg], m.ref, "")
				if err != nil {
					return fmt.Errorf("Parse entity struct: %w", err)
				}

				var retType string
				switch refMapping.Type {
				case ReferenceTable:
					retType = fmt.Sprintf("map[%s]%s", refMapping.Identifier().Type.Name, refMapping.Name)
				case MapTable:
					retType = "map[string]string"
				}

				rets = fmt.Sprintf("(%s, error)", retType)
			}
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
			if m.ref == "" {
				entityCreate, ok := m.config["struct"]
				if !ok {
					entityCreate = mapping.Name
				}
				comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
				args = fmt.Sprintf("object %s", entityType(m.pkg, entityCreate))
				rets = "(int64, error)"
			} else {
				comment = fmt.Sprintf("adds a new %s %s to the database.", m.entity, m.ref)
				rets = "error"

				refMapping, err := Parse(m.packages[m.pkg], m.ref, "")
				if err != nil {
					return fmt.Errorf("Parse entity struct: %w", err)
				}

				switch refMapping.Type {
				case ReferenceTable:
					args = fmt.Sprintf("%sID int64, %s %s", lex.Minuscule(lex.Camel(m.entity)), lex.Minuscule(m.ref), m.ref)
				case MapTable:
					args = fmt.Sprintf("%sID int64, %s map[string]string", lex.Minuscule(lex.Camel(m.entity)), lex.Minuscule(m.ref))
				}
			}
		case "CreateOrReplace":
			entityCreate, ok := m.config["struct"]
			if !ok {
				entityCreate = mapping.Name
			}
			comment = fmt.Sprintf("adds a new %s to the database.", m.entity)
			args = fmt.Sprintf("object %s", entityType(m.pkg, entityCreate))
			rets = "(int64, error)"
		case "Rename":
			comment = fmt.Sprintf("renames the %s matching the given key parameters.", m.entity)
			args = mapping.FieldArgs(mapping.NaturalKey(), "to string")
			rets = "error"
		case "Update":
			if m.ref == "" {
				entityUpdate, ok := m.config["struct"]
				if !ok {
					entityUpdate = mapping.Name
				}
				comment = fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
				args = mapping.FieldArgs(mapping.NaturalKey(), fmt.Sprintf("object %s", entityType(m.pkg, entityUpdate)))
				rets = "error"
			} else {
				comment = fmt.Sprintf("updates the %s %s matching the given key parameters.", m.entity, m.ref)
				rets = "error"

				refMapping, err := Parse(m.packages[m.pkg], m.ref, "")
				if err != nil {
					return fmt.Errorf("Parse entity struct: %w", err)
				}

				switch refMapping.Type {
				case ReferenceTable:
					args = fmt.Sprintf("%sID int64, %s map[%s]%s", m.entity, lex.Minuscule(lex.Plural(m.ref)), refMapping.Identifier().Type.Name, m.ref)
				case MapTable:
					args = fmt.Sprintf("%sID int64, %s map[string]string", m.entity, lex.Minuscule(lex.Plural(m.ref)))
				}
			}
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
	}

	m.begin(buf, comment, args, rets, isInterface)
	return nil
}

func (m *Method) begin(buf *file.Buffer, comment string, args string, rets string, isInterface bool) error {
	mapping, err := Parse(m.packages[m.pkg], lex.Camel(m.entity), m.kind)
	if err != nil {
		return fmt.Errorf("Parse entity struct: %w", err)
	}
	name := ""
	entity := lex.Camel(m.entity)

	if mapping.Type == AssociationTable {
		parent := m.config["struct"]
		ref := strings.Replace(entity, parent, "", -1)
		switch operation(m.kind) {
		case "GetMany":
			name = fmt.Sprintf("Get%s%s", parent, lex.Plural(ref))
		case "Create":
			name = fmt.Sprintf("Create%s%s", parent, ref)
		case "Update":
			name = fmt.Sprintf("Update%s%s", parent, lex.Plural(ref))
		case "DeleteMany":
			name = fmt.Sprintf("Delete%s%s", parent, lex.Plural(ref))
		}
	} else {
		entity = entity + m.ref
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
			if mapping.Type == ReferenceTable || m.ref != "" {
				entity = lex.Plural(entity)
			}

			name = fmt.Sprintf("Update%s", entity)
		case "DeleteOne":
			name = fmt.Sprintf("Delete%s", entity)
		case "DeleteMany":
			name = fmt.Sprintf("Delete%s", lex.Plural(entity))
		default:
			name = fmt.Sprintf("%s%s", entity, m.kind)
		}
	}

	receiver := fmt.Sprintf("c %s", dbTxType(m.db))

	buf.L("// %s %s", name, comment)
	buf.L("// generator: %s %s", m.entity, m.kind)

	if isInterface {
		buf.L("%s(%s) %s", name, args, rets)
	} else {
		buf.L("func (%s) %s(%s) %s {", receiver, name, args, rets)
	}

	return nil
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
