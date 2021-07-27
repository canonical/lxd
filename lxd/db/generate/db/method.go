package db

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/lxc/lxd/lxd/db/generate/file"
	"github.com/lxc/lxd/lxd/db/generate/lex"
	"github.com/lxc/lxd/shared"
	"github.com/pkg/errors"
)

// Method generates a code snippet for a particular database query method.
type Method struct {
	db            string                  // Target database (cluster or node)
	pkg           string                  // Package where the entity struct is declared.
	entity        string                  // Name of the database entity
	kind          string                  // Kind of statement to generate
	config        map[string]string       // Configuration parameters
	packages      map[string]*ast.Package // Packages to perform for struct declaration lookups
	buf           *file.Buffer            // File buffer to write to
	mapping       *Mapping                // Mapping for the method
	activeFilters []*Field                // Filters to apply to the database query
}

// NewMethod return a new method code snippet for executing a certain mapping.
func NewMethod(database, pkg, entity, kind string, config map[string]string) (*Method, error) {
	packages, err := Packages()
	if err != nil {
		return nil, err
	}

	var mapping *Mapping

	entityCreate, ok := config["struct"]
	if !ok {
		mapping, err = Parse(packages[pkg], lex.Camel(entity), kind)
		if err != nil {
			return nil, errors.Wrap(err, "Parse entity struct")
		}
	} else {
		mapping, err = Parse(packages[pkg], entityCreate, kind)
		if err != nil {
			return nil, errors.Wrap(err, "Parse entity struct")
		}
	}

	coreFields := []string{}
	methodKind := kind
	if strings.Contains(kind, "-by-") {
		index := strings.Index(kind, "-by-")
		coreFields = strings.Split(kind[index+len("-by-"):], "-and-")
		methodKind = kind[:index]
	}

	activeFilters := []*Field{}
	for _, fieldName := range coreFields {
		field := mapping.FieldByName(fieldName)
		if field == nil {
			return nil, fmt.Errorf("Argument %q is not a valid field for struct %q", fieldName, lex.Camel(entity))
		}

		activeFilters = append(activeFilters, field)
	}

	method := &Method{
		db:            database,
		pkg:           pkg,
		entity:        entity,
		kind:          methodKind,
		config:        config,
		packages:      packages,
		mapping:       mapping,
		activeFilters: activeFilters,
	}

	return method, nil
}

// Generate the desired method.
func (m *Method) Generate(buf *file.Buffer) error {
	m.buf = buf
	if strings.HasSuffix(m.kind, "Ref") {
		return m.ref()
	}

	switch m.kind {
	case "URIs":
		return m.uris()
	case "List":
		return m.list()
	case "Get":
		return m.get()
	case "ID":
		return m.id()
	case "Exists":
		return m.exists()
	case "Create":
		return m.create(false)
	case "CreateOrReplace":
		return m.create(true)
	case "Rename":
		return m.rename()
	case "Update":
		return m.update()
	case "DeleteOne":
		return m.delete(true)
	case "DeleteMany":
		return m.delete(false)
	}

	return fmt.Errorf("Unknown method kind '%s'", m.kind)
}

func (m *Method) uris() error {
	comment := fmt.Sprintf("returns all available %s URIs.", m.entity)
	args := m.argsWithFilter()
	rets := "([]string, error)"

	m.begin(comment, args, rets)
	defer m.end()

	m.addFiltersToStmt()

	m.buf.L("code := %s.EntityTypes[%q]", m.db, m.entity)
	m.buf.L("formatter := %s.EntityFormatURIs[code]", m.db)
	m.buf.N()
	m.buf.L("return query.SelectURIs(stmt, formatter, args...)")

	return nil
}

func (m *Method) list() error {
	// Go type name the objects to return (e.g. api.Foo).
	typ := entityType(m.pkg, m.entity)

	comment := fmt.Sprintf("returns all available %s.", lex.Plural(m.entity))
	args := m.argsWithFilter()
	rets := fmt.Sprintf("(%s, error)", lex.Slice(typ))

	m.begin(comment, args, rets)
	defer m.end()

	m.buf.L("// Result slice.")
	m.buf.L("objects := make(%s, 0)", lex.Slice(typ))
	m.buf.N()
	m.buf.L("// Pick the prepared statement and arguments to use based on active criteria.")

	m.addFiltersToStmt()

	m.buf.N()
	m.buf.L("// Dest function for scanning a row.")
	m.buf.L("dest := %s", destFunc("objects", typ, m.mapping.ColumnFields()))
	m.buf.N()
	m.buf.L("// Select.")
	m.buf.L("err := query.SelectObjects(stmt, dest, args...)")
	m.buf.L("if err != nil {")
	m.buf.L("        return nil, errors.Wrap(err, \"Failed to fetch %s\")", lex.Plural(m.entity))
	m.buf.L("}")
	m.buf.N()

	// Fill reference fields.
	nk := m.mapping.NaturalKey()
	for _, field := range m.mapping.RefFields() {
		err := m.fillSliceReferenceField(nk, field)
		if err != nil {
			return err
		}
	}

	m.buf.L("return objects, nil")
	return nil
}

func (m *Method) get() error {
	comment := fmt.Sprintf("returns the %s with the given key.", m.entity)

	typ := entityType(m.pkg, m.entity)

	args := m.argsWithFilter()
	rets := fmt.Sprintf("(%s, error)", lex.Star(typ))

	method := m.filterSequence(lex.Plural(lex.Camel(m.entity)))

	m.begin(comment, args, rets)
	defer m.end()

	m.buf.L("objects, err := c.Get%s(%s)", method, m.paramsWithFilter())
	m.buf.L("if err != nil {")
	m.buf.L("        return nil, errors.Wrap(err, \"Failed to fetch %s\")", lex.Camel(m.entity))
	m.buf.L("}")
	m.buf.N()
	m.buf.L("switch len(objects) {")
	m.buf.L("case 0:")
	m.buf.L("        return nil, ErrNoSuchObject")
	m.buf.L("case 1:")
	m.buf.L("        return &objects[0], nil")
	m.buf.L("default:")
	m.buf.L("        return nil, fmt.Errorf(\"More than one %s matches\")", m.entity)
	m.buf.L("}")

	return nil
}

func (m *Method) ref() error {
	nk := m.mapping.NaturalKey()

	name := m.kind[:len(m.kind)-len("Ref")]
	field := m.mapping.FieldByName(name)

	var typ string
	var retTyp string
	var destType string
	var destFields []*Field

	if field.Type.Code == TypeSlice {
		retTyp = field.Type.Name
		typ = lex.Element(field.Type.Name)
		if IsColumnType(typ) {
			destType = "struct {\n"
			for _, field := range nk {
				destType += fmt.Sprintf("%s %s\n", field.Name, field.Type.Name)
			}
			destType += fmt.Sprintf("Value %s\n}", typ)
			valueField := Field{Name: "Value"}
			destFields = append(nk, &valueField)
		} else {
			// TODO
			destType = typ
			destFields = nil
		}
	} else if field.Type.Code == TypeMap && field.Type.Name == "map[string]string" {
		retTyp = field.Type.Name
		// Config reference
		destType = "struct {\n"
		for _, field := range nk {
			destType += fmt.Sprintf("%s %s\n", field.Name, field.Type.Name)
		}
		destType += fmt.Sprintf("Key string\n")
		destType += fmt.Sprintf("Value string\n}")
		keyField := Field{Name: "Key"}
		valueField := Field{Name: "Value"}
		destFields = append(nk, &keyField, &valueField)
	} else if field.Type.Code == TypeMap && field.Type.Name == "map[string]map[string]string" {
		retTyp = field.Type.Name
		// Device reference
		destType = "struct {\n"
		for _, field := range nk {
			destType += fmt.Sprintf("%s %s\n", field.Name, field.Type.Name)
		}
		destType += fmt.Sprintf("Device string\n")
		destType += fmt.Sprintf("Type int\n")
		destType += fmt.Sprintf("Key string\n")
		destType += fmt.Sprintf("Value string\n}")
		deviceField := Field{Name: "Device"}
		typeField := Field{Name: "Type"}
		keyField := Field{Name: "Key"}
		valueField := Field{Name: "Value"}
		destFields = append(nk, &deviceField, &typeField, &keyField, &valueField)

	} else {
		return fmt.Errorf("Unsupported ref type %q", field.Type.Name)
	}

	comment := fmt.Sprintf("returns entities used by %s.", lex.Plural(m.entity))

	// The type of the returned index takes into account composite natural
	// keys.
	indexTyp := indexType(nk, retTyp)

	args := m.args()
	rets := fmt.Sprintf("(%s, error)", indexTyp)

	m.begin(comment, args, rets)
	defer m.end()

	m.buf.L("// Result slice.")
	m.buf.L("objects := make(%s, 0)", lex.Slice(destType))
	m.buf.N()
	filterNames := []string{}
	for _, f := range m.activeFilters {
		filterNames = append(filterNames, f.Name)
	}

	m.buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, m.kind, filterNames...))
	m.buf.L("args := []interface{}{")
	for _, filter := range m.activeFilters {
		name := filter.Name
		if name == "Type" {
			name = lex.Camel(m.entity) + name
		}
		m.buf.L("%s,", lex.Minuscule(name))
	}
	m.buf.L("}")

	m.buf.L("// Dest function for scanning a row.")
	m.buf.L("dest := %s", destFunc("objects", destType, destFields))
	m.buf.N()
	m.buf.L("// Select.")
	m.buf.L("err := query.SelectObjects(stmt, dest, args...)")
	m.buf.L("if err != nil {")
	m.buf.L("        return nil, errors.Wrap(err, \"Failed to fetch %s ref for %s\")", typ, lex.Plural(m.entity))
	m.buf.L("}")
	m.buf.N()

	m.buf.L("// Build index by primary name.")
	m.buf.L("index := %s{}", indexTyp)
	m.buf.N()
	m.buf.L("for _, object := range objects {")
	needle := ""
	for i, key := range nk[:len(nk)-1] {
		needle += fmt.Sprintf("[object.%s]", key.Name)

		subIndexTyp := indexType(nk[i+1:], retTyp)
		m.buf.L("        _, ok%d := index%s", i, needle)
		m.buf.L("        if !ok%d {", i)
		m.buf.L("                subIndex := %s{}", subIndexTyp)
		m.buf.L("                index%s = subIndex", needle)
		m.buf.L("        }")
		m.buf.N()
	}

	needle += fmt.Sprintf("[object.%s]", nk[len(nk)-1].Name)
	m.buf.L("        item, ok := index%s", needle)
	m.buf.L("        if !ok {")
	m.buf.L("                item = %s{}", retTyp)
	m.buf.L("        }")
	m.buf.N()
	if field.Type.Code == TypeSlice && IsColumnType(typ) {
		m.buf.L("        index%s = append(item, object.Value)", needle)
	} else if field.Type.Code == TypeMap && field.Type.Name == "map[string]string" {
		m.buf.L("        index%s = item", needle)
		m.buf.L("        item[object.Key] = object.Value")
	} else if field.Type.Code == TypeMap && field.Type.Name == "map[string]map[string]string" {
		m.buf.L("        index%s = item", needle)
		m.buf.L("        config, ok := item[object.Device]")
		m.buf.L("        if !ok {")
		m.buf.L("                // First time we see this device, let's int the config")
		m.buf.L("                // and add the type.")
		m.buf.L("                deviceType, err := deviceTypeToString(object.Type)")
		m.buf.L("                if err != nil {")
		m.buf.L("                        return nil, errors.Wrapf(")
		m.buf.L("                            err, \"unexpected device type code '%%d'\", object.Type)")
		m.buf.L("                }")
		m.buf.L("                config = map[string]string{}")
		m.buf.L("                config[\"type\"] = deviceType")
		m.buf.L("                item[object.Device] = config")
		m.buf.L("        }")
		m.buf.L("        if object.Key != \"\" {")
		m.buf.L("                config[object.Key] = object.Value")
		m.buf.L("        }")
	} else {
	}
	m.buf.L("}")
	m.buf.N()
	m.buf.L("return index, nil")

	return nil
}

// Populate a field consisting of a slice of objects referencing the
// entity. This information is available by joining a the view or table
// associated with the type of the referenced objects, which must contain the
// natural key of the entity.
func (m *Method) fillSliceReferenceField(nk []*Field, field *Field) error {
	objectsVar := fmt.Sprintf("%sObjects", lex.Minuscule(field.Name))
	methodName := m.filterSequence(fmt.Sprintf("%s%sRef", lex.Camel(m.entity), field.Name))

	m.buf.L("// Fill field %s.", field.Name)
	m.buf.L("%s, err := c.%s(%s)", objectsVar, methodName, m.params())
	m.buf.L("if err != nil {")
	m.buf.L("        return nil, errors.Wrap(err, \"Failed to fetch field %s\")", field.Name)
	m.buf.L("}")
	m.buf.N()
	m.buf.L("for i := range objects {")
	needle := ""
	for i, key := range nk[:len(nk)-1] {
		needle += fmt.Sprintf("[objects[i].%s]", key.Name)
		subIndexTyp := indexType(nk[i+1:], field.Type.Name)
		m.buf.L("        _, ok%d := %s%s", i, objectsVar, needle)
		m.buf.L("        if !ok%d {", i)
		m.buf.L("                subIndex := %s{}", subIndexTyp)
		m.buf.L("                %s%s = subIndex", objectsVar, needle)
		m.buf.L("        }")
		m.buf.N()
	}

	needle += fmt.Sprintf("[objects[i].%s]", nk[len(nk)-1].Name)
	m.buf.L("        value := %s%s", objectsVar, needle)
	m.buf.L("        if value == nil {")
	m.buf.L("                value = %s{}", field.Type.Name)
	m.buf.L("        }")
	if field.Name == "UsedBy" {
		m.buf.L("        for j := range value {")
		m.buf.L("                if len(value[j]) > 12 && value[j][len(value[j])-12:] == \"&target=none\" {")
		m.buf.L("                         value[j] = value[j][0:len(value[j])-12]")
		m.buf.L("                }")
		m.buf.L("                if len(value[j]) > 16 && value[j][len(value[j])-16:] == \"?project=default\" {")
		m.buf.L("                         value[j] = value[j][0:len(value[j])-16]")
		m.buf.L("                }")
		m.buf.L("        }")
	}
	m.buf.L("        objects[i].%s = value", field.Name)
	m.buf.L("}")
	m.buf.N()

	return nil
}

func (m *Method) id() error {
	nk := m.mapping.NaturalKey()

	comment := fmt.Sprintf("return the ID of the %s with the given key.", m.entity)
	args := FieldArgs(nk)
	rets := "(int64, error)"

	m.begin(comment, args, rets)
	defer m.end()

	m.buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "ID"))
	m.buf.L("rows, err := stmt.Query(%s)", FieldParams(nk))
	m.buf.L("if err != nil {")
	m.buf.L("        return -1, errors.Wrap(err, \"Failed to get %s ID\")", m.entity)
	m.buf.L("}")
	m.buf.L("defer rows.Close()")
	m.buf.N()
	m.buf.L("// Ensure we read one and only one row.")
	m.buf.L("if !rows.Next() {")
	m.buf.L("        return -1, ErrNoSuchObject")
	m.buf.L("}")
	m.buf.L("var id int64")
	m.buf.L("err = rows.Scan(&id)")
	m.buf.L("if err != nil {")
	m.buf.L("        return -1, errors.Wrap(err, \"Failed to scan ID\")")
	m.buf.L("}")
	m.buf.L("if rows.Next() {")
	m.buf.L("        return -1, fmt.Errorf(\"More than one row returned\")")
	m.buf.L("}")
	m.buf.L("err = rows.Err()")
	m.buf.L("if err != nil {")
	m.buf.L("        return -1, errors.Wrap(err, \"Result set failure\")")
	m.buf.L("}")
	m.buf.N()
	m.buf.L("return id, nil")

	return nil
}

func (m *Method) exists() error {
	nk := m.mapping.NaturalKey()

	comment := fmt.Sprintf("checks if a %s with the given key exists.", m.entity)
	args := FieldArgs(nk)
	rets := "(bool, error)"

	m.begin(comment, args, rets)
	defer m.end()

	m.buf.L("_, err := c.Get%sID(%s)", lex.Camel(m.entity), FieldParams(nk))
	m.buf.L("if err != nil {")
	m.buf.L("        if err == ErrNoSuchObject {")
	m.buf.L("                return false, nil")
	m.buf.L("        }")
	m.buf.L("        return false, err")
	m.buf.L("}")
	m.buf.N()
	m.buf.L("return true, nil")

	return nil
}

func (m *Method) create(replace bool) error {
	comment := fmt.Sprintf("adds a new %s to the database.", m.entity)
	args := fmt.Sprintf("object %s", entityType(m.pkg, m.mapping.Name))
	rets := "(int64, error)"

	m.begin(comment, args, rets)
	defer m.end()

	nk := m.mapping.NaturalKey()
	nkParams := make([]string, len(nk))
	for i, field := range nk {
		nkParams[i] = fmt.Sprintf("object.%s", field.Name)
	}

	kind := "create"
	if replace {
		kind = "create_or_replace"
	} else {
		m.buf.L("// Check if a %s with the same key exists.", m.entity)
		m.buf.L("exists, err := c.%sExists(%s)", lex.Camel(m.entity), strings.Join(nkParams, ", "))
		m.buf.L("if err != nil {")
		m.buf.L("        return -1, errors.Wrap(err, \"Failed to check for duplicates\")")
		m.buf.L("}")
		m.buf.L("if exists {")
		m.buf.L("        return -1, fmt.Errorf(\"This %s already exists\")", m.entity)
		m.buf.L("}")
		m.buf.N()
	}

	fields := m.mapping.ColumnFields("ID")
	m.buf.L("args := make([]interface{}, %d)", len(fields))
	m.buf.N()

	m.buf.L("// Populate the statement arguments. ")
	for i, field := range fields {
		m.buf.L("args[%d] = object.%s", i, field.Name)
	}

	m.buf.N()

	m.buf.L("// Prepared statement to use. ")
	m.buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, kind))
	m.buf.N()
	m.buf.L("// Execute the statement. ")
	m.buf.L("result, err := stmt.Exec(args...)")
	m.buf.L("if err != nil {")
	m.buf.L("        return -1, errors.Wrap(err, \"Failed to create %s\")", m.entity)
	m.buf.L("}")
	m.buf.N()
	m.buf.L("id, err := result.LastInsertId()")
	m.buf.L("if err != nil {")
	m.buf.L("        return -1, errors.Wrap(err, \"Failed to fetch %s ID\")", m.entity)
	m.buf.L("}")
	m.buf.N()

	fields = m.mapping.RefFields()
	for _, field := range fields {
		if field.Type.Name == "map[string]string" {
			m.buf.L("// Insert config reference. ")
			m.buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "createConfigRef"))
			m.buf.L("for key, value := range object.%s {", field.Name)
			m.buf.L("        _, err := stmt.Exec(id, key, value)")
			m.buf.L("        if err != nil {")
			m.buf.L("                return -1, errors.Wrap(err, \"Insert config for %s\")", m.entity)
			m.buf.L("        }")
			m.buf.L("}")
			m.buf.N()
		}
		if field.Type.Name == "map[string]map[string]string" {
			m.buf.L("// Insert devices reference. ")
			m.buf.L("for name, config := range object.%s {", field.Name)
			m.buf.L("        typ, ok := config[\"type\"]")
			m.buf.L("        if !ok {")
			m.buf.L("                return -1, fmt.Errorf(\"No type for device %%s\", name)")
			m.buf.L("        }")
			m.buf.L("        typCode, err := deviceTypeToInt(typ)")
			m.buf.L("        if err != nil {")
			m.buf.L("                return -1, errors.Wrapf(err, \"Device type code for %%s\", typ)")
			m.buf.L("        }")
			m.buf.L("        stmt = c.stmt(%s)", stmtCodeVar(m.entity, "createDevicesRef"))
			m.buf.L("        result, err := stmt.Exec(id, name, typCode)")
			m.buf.L("        if err != nil {")
			m.buf.L("                return -1, errors.Wrapf(err, \"Insert device %%s\", name)")
			m.buf.L("        }")
			m.buf.L("        deviceID, err := result.LastInsertId()")
			m.buf.L("        if err != nil {")
			m.buf.L("                return -1, errors.Wrap(err, \"Failed to fetch device ID\")")
			m.buf.L("        }")
			m.buf.L("        stmt = c.stmt(%s)", stmtCodeVar(m.entity, "createDevicesConfigRef"))
			m.buf.L("        for key, value := range config {")
			m.buf.L("                _, err := stmt.Exec(deviceID, key, value)")
			m.buf.L("                if err != nil {")
			m.buf.L("                        return -1, errors.Wrap(err, \"Insert config for %s\")", m.entity)
			m.buf.L("                }")
			m.buf.L("        }")
			m.buf.L("}")
			m.buf.N()
		}
		if field.Name == "Profiles" {
			// TODO: get rid of the special case
			m.buf.L("// Insert profiles reference. ")
			m.buf.L("err = addProfilesToInstance(c.tx, int(id), object.Project, object.Profiles)")
			m.buf.L("if err != nil {")
			m.buf.L("        return -1, errors.Wrap(err, \"Insert profiles for %s\")", m.entity)
			m.buf.L("}")
		}
	}

	m.buf.L("return id, nil")

	return nil
}

func (m *Method) rename() error {
	nk := m.mapping.NaturalKey()

	comment := fmt.Sprintf("renames the %s matching the given key parameters.", m.entity)
	args := FieldArgs(nk) + ", to string"
	rets := "error"

	m.begin(comment, args, rets)
	defer m.end()

	m.buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "rename"))
	m.buf.L("result, err := stmt.Exec(%s)", "to, "+FieldParams(nk))
	m.buf.L("if err != nil {")
	m.buf.L("        return errors.Wrap(err, \"Rename %s\")", m.entity)
	m.buf.L("}")
	m.buf.N()
	m.buf.L("n, err := result.RowsAffected()")
	m.buf.L("if err != nil {")
	m.buf.L("        return errors.Wrap(err, \"Fetch affected rows\")")
	m.buf.L("}")
	m.buf.L("if n != 1 {")
	m.buf.L("        return fmt.Errorf(\"Query affected %%d rows instead of 1\", n)")
	m.buf.L("}")

	m.buf.L("return nil")

	return nil
}

func (m *Method) update() error {
	// Support using a different structure or package to pass arguments to Create.
	entityUpdate, ok := m.config["struct"]
	if !ok {
		entityUpdate = entityPut(m.entity)
	}

	nk := m.mapping.NaturalKey()

	comment := fmt.Sprintf("updates the %s matching the given key parameters.", m.entity)
	args := FieldArgs(nk) + fmt.Sprintf(", object %s", entityType(m.pkg, entityUpdate))
	rets := "error"

	m.begin(comment, args, rets)
	defer m.end()

	updateMapping, err := Parse(m.packages[m.pkg], entityUpdate, m.kind)
	if err != nil {
		return errors.Wrap(err, "Parse entity struct")
	}
	fields := updateMapping.ColumnFields("ID") // This exclude the ID column, which is autogenerated.

	params := make([]string, len(fields))

	for i, field := range fields {
		params[i] = fmt.Sprintf("object.%s", field.Name)
	}

	//m.buf.L("id, err := c.Get%s(%s)", lex.Camel(m.entity), FieldArgs(nk))
	m.buf.L("id, err := c.Get%sID(%s)", lex.Camel(m.entity), FieldParams(nk))
	m.buf.L("if err != nil {")
	m.buf.L("        return errors.Wrap(err, \"Get %s\")", m.entity)
	m.buf.L("}")
	m.buf.N()
	m.buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, "update"))
	m.buf.L("result, err := stmt.Exec(%s)", strings.Join(params, ", ")+", id")
	m.buf.L("if err != nil {")
	m.buf.L("        return errors.Wrap(err, \"Update %s\")", m.entity)
	m.buf.L("}")
	m.buf.N()
	m.buf.L("n, err := result.RowsAffected()")
	m.buf.L("if err != nil {")
	m.buf.L("        return errors.Wrap(err, \"Fetch affected rows\")")
	m.buf.L("}")
	m.buf.L("if n != 1 {")
	m.buf.L("        return fmt.Errorf(\"Query updated %%d rows instead of 1\", n)")
	m.buf.L("}")
	m.buf.N()

	fields = m.mapping.RefFields()
	for _, field := range fields {
		switch field.Name {
		case "Config":
			m.buf.L("// Delete current config. ")
			m.buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "deleteConfigRef"))
			m.buf.L("_, err = stmt.Exec(id)")
			m.buf.L("if err != nil {")
			m.buf.L("        return errors.Wrap(err, \"Delete current config\")")
			m.buf.L("}")
			m.buf.N()
			m.buf.L("// Insert config reference. ")
			m.buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "createConfigRef"))
			m.buf.L("for key, value := range object.%s {", field.Name)
			m.buf.L("        if value == \"\" {")
			m.buf.L("                continue")
			m.buf.L("        }")
			m.buf.L("        _, err := stmt.Exec(id, key, value)")
			m.buf.L("        if err != nil {")
			m.buf.L("                return errors.Wrap(err, \"Insert config for %s\")", m.entity)
			m.buf.L("        }")
			m.buf.L("}")
			m.buf.N()
		case "Devices":
			m.buf.L("// Delete current devices. ")
			m.buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "deleteDevicesRef"))
			m.buf.L("_, err = stmt.Exec(id)")
			m.buf.L("if err != nil {")
			m.buf.L("        return errors.Wrap(err, \"Delete current devices\")")
			m.buf.L("}")
			m.buf.N()
			m.buf.L("// Insert devices reference. ")
			m.buf.L("for name, config := range object.%s {", field.Name)
			m.buf.L("        typ, ok := config[\"type\"]")
			m.buf.L("        if !ok {")
			m.buf.L("                return fmt.Errorf(\"No type for device %%s\", name)")
			m.buf.L("        }")
			m.buf.L("        typCode, err := deviceTypeToInt(typ)")
			m.buf.L("        if err != nil {")
			m.buf.L("                return errors.Wrapf(err, \"Device type code for %%s\", typ)")
			m.buf.L("        }")
			m.buf.L("        stmt = c.stmt(%s)", stmtCodeVar(m.entity, "createDevicesRef"))
			m.buf.L("        result, err := stmt.Exec(id, name, typCode)")
			m.buf.L("        if err != nil {")
			m.buf.L("                return errors.Wrapf(err, \"Insert device %%s\", name)")
			m.buf.L("        }")
			m.buf.L("        deviceID, err := result.LastInsertId()")
			m.buf.L("        if err != nil {")
			m.buf.L("                return errors.Wrap(err, \"Failed to fetch device ID\")")
			m.buf.L("        }")
			m.buf.L("        stmt = c.stmt(%s)", stmtCodeVar(m.entity, "createDevicesConfigRef"))
			m.buf.L("        for key, value := range config {")
			m.buf.L("                if value == \"\" {")
			m.buf.L("                        continue")
			m.buf.L("                }")
			m.buf.L("                _, err := stmt.Exec(deviceID, key, value)")
			m.buf.L("                if err != nil {")
			m.buf.L("                        return errors.Wrap(err, \"Insert config for %s\")", m.entity)
			m.buf.L("                }")
			m.buf.L("        }")
			m.buf.L("}")
			m.buf.N()
		case "Profiles":
			m.buf.L("// Delete current profiles. ")
			m.buf.L("stmt = c.stmt(%s)", stmtCodeVar(m.entity, "deleteProfilesRef"))
			m.buf.L("_, err = stmt.Exec(id)")
			m.buf.L("if err != nil {")
			m.buf.L("        return errors.Wrap(err, \"Delete current profiles\")")
			m.buf.L("}")
			m.buf.N()
			m.buf.L("// Insert profiles reference. ")
			m.buf.L("err = addProfilesToInstance(c.tx, int(id), object.Project, object.Profiles)")
			m.buf.L("if err != nil {")
			m.buf.L("        return errors.Wrap(err, \"Insert profiles for %s\")", m.entity)
			m.buf.L("}")
		}
	}

	m.buf.L("return nil")

	return nil
}

func (m *Method) delete(deleteOne bool) error {
	comment := fmt.Sprintf("deletes the %s matching the given key parameters.", m.entity)
	args := m.argsWithFilter()
	rets := "error"

	m.begin(comment, args, rets)
	defer m.end()

	m.addFiltersToStmt()

	m.buf.L("result, err := stmt.Exec(args...)")
	m.buf.L("if err != nil {")
	m.buf.L("        return errors.Wrap(err, \"Delete %s\")", m.entity)
	m.buf.L("}")
	m.buf.N()

	if deleteOne {
		m.buf.L("n, err := result.RowsAffected()")
	} else {
		m.buf.L("_, err = result.RowsAffected()")
	}

	m.buf.L("if err != nil {")
	m.buf.L("        return errors.Wrap(err, \"Fetch affected rows\")")
	m.buf.L("}")

	if deleteOne {
		m.buf.L("if n != 1 {")
		m.buf.L("        return fmt.Errorf(\"Query deleted %%d rows instead of 1\", n)")
		m.buf.L("}")
	}

	m.buf.N()
	m.buf.L("return nil")
	return nil
}

func (m *Method) begin(comment string, args string, rets string) {
	name := ""
	entity := lex.Camel(m.entity)
	kind := strings.Replace(m.kind, "-", "_", -1)
	switch m.kind {
	case "URIs":
		name = fmt.Sprintf("Get%sURIs", entity)
	case "List":
		name = fmt.Sprintf("Get%s", lex.Plural(entity))
	case "Get":
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
		name = fmt.Sprintf("Delete%s", lex.Plural(entity))
	default:
		name = fmt.Sprintf("%s%s", entity, lex.Camel(kind))
	}
	receiver := fmt.Sprintf("c %s", dbTxType(m.db))
	name = m.filterSequence(name)

	keyword := m.kind
	if len(m.activeFilters) > 0 {
		filterNames := []string{}
		for _, filter := range m.activeFilters {
			filterNames = append(filterNames, filter.Name)
		}
		keyword += "-by-" + strings.Join(filterNames, "-and-")
	}

	m.buf.L("// %s %s", name, comment)
	m.buf.L("// generator: %s %s", m.entity, keyword)
	m.buf.L("func (%s) %s(%s) %s {", receiver, name, args, rets)
}

func (m *Method) end() {
	m.buf.L("}")
}

// args returns the arguments for the signature of the method.
func (m *Method) args() string {
	args := ""
	for i, filter := range m.activeFilters {
		if i > 0 {
			args += ", "
		}
		name := filter.Name
		if name == "Type" {
			name = lex.Camel(m.entity) + name
		}
		args = fmt.Sprintf("%s%s %s", args, lex.Minuscule(name), filter.Type.Name)
	}

	return args
}

// args returns the arguments for the signature of the method.
func (m *Method) argsWithFilter() string {
	args := m.args()
	if len(args) > 0 {
		args += ", "
	}
	return fmt.Sprintf("%sfilter %s", args, entityFilter(m.entity))
}

// params returns the arguments of the method as a string of parameters.
func (m *Method) params() string {
	params := ""
	for i, filter := range m.activeFilters {
		if i > 0 {
			params += ", "
		}
		name := filter.Name
		if name == "Type" {
			name = lex.Camel(m.entity) + name
		}
		params += lex.Minuscule(name)
	}
	return params
}

func (m *Method) paramsWithFilter() string {
	params := m.params()
	if len(params) > 0 {
		params += ", "
	}
	return params + "filter"
}

// returns the string sequence for variable names: PrefixByFilter1AndFilter2
func (m *Method) filterSequence(prefix string) string {
	sequence := prefix
	for i, filter := range m.activeFilters {
		if i == 0 {
			sequence += "By"
		}
		if i > 0 {
			sequence += "And"
		}
		sequence += lex.Camel(filter.Name)
	}
	return sequence
}

func (m *Method) addFiltersToStmt() {
	filterNames := []string{}
	for _, f := range m.activeFilters {
		filterNames = append(filterNames, f.Name)
	}

	m.buf.L("stmt := c.stmt(%s)", stmtCodeVar(m.entity, m.kind, filterNames...))
	m.buf.L("args := []interface{}{")
	for _, filter := range m.activeFilters {
		name := filter.Name
		if name == "Type" {
			name = lex.Camel(m.entity) + name
		}
		m.buf.L("%s,", lex.Minuscule(name))
	}
	m.buf.L("}")

	if len(m.mapping.Filters) > 0 {
		m.buf.L("stmtStr := cluster.GetRegisteredStmt(%s)", stmtCodeVar(m.entity, m.kind, filterNames...))
	}
	for _, filter := range m.mapping.Filters {
		zero := filter.ZeroValue()
		m.buf.L("if filter.%s != %s {", filter.Name, zero)
		m.buf.L("  args = append(args, filter.%s)", filter.Name)
		m.buf.L("  stmtParts := strings.Split(stmtStr, \"ORDER BY\")")
		m.buf.L("  stmtBody := stmtParts[0]")
		m.buf.L("  stmtOrderBy := \"\"")
		m.buf.L("  if len(stmtParts) == 2 {")
		m.buf.L("    stmtOrderBy = \" ORDER BY \" + stmtParts[1]")
		m.buf.L("  }")
		m.buf.L("  if strings.Contains(stmtBody, \"WHERE\") {")
		m.buf.L("    stmtBody += \" AND \" ")
		m.buf.L("  } else {")

		comparator := "="
		if comp := filter.Config.Get("comparison"); shared.StringInSlice("like", strings.Split(comp, ",")) {
			comparator = "LIKE"
		}

		m.buf.L("    stmtBody += \" WHERE \"")
		m.buf.L("  }")
		m.buf.L("  fullStmt := fmt.Sprintf(\"%%s%%s %s ?%%s\", stmtBody, \"%s\", stmtOrderBy)", comparator, lex.Snake(filter.Name))
		m.buf.L("  stmt = c.stmt(cluster.RegisterStmtIfNew(fullStmt))")
		m.buf.L("}")
		m.buf.N()
	}
}
