package db

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared/generate/lex"
)

// Return the table name for the given database entity.
func entityTable(entity string) string {
	return lex.Plural(lex.Minuscule(entity))
}

// Return Go type of the given database entity.
func entityType(pkg string, entity string) string {
	typ := lex.Capital(entity)
	if pkg != "db" {
		typ = pkg + "." + typ
	}
	return typ
}

// Return the name of the Filter struct for the given database entity.
func entityFilter(entity string) string {
	return fmt.Sprintf("%sFilter", lex.Capital(entity))
}

// Return the name of the Post struct for the given entity.
func entityPost(entity string) string {
	return fmt.Sprintf("%sPost", lex.Capital(lex.Plural(entity)))
}

// Return the name of the Put struct for the given entity.
func entityPut(entity string) string {
	return fmt.Sprintf("%sPut", lex.Capital(entity))
}

// Return the name of the global variable holding the registration code for
// the given kind of statement aganst the given entity.
func stmtCodeVar(entity string, kind string, filters ...string) string {
	name := fmt.Sprintf("%s%s", entity, lex.Camel(kind))

	if len(filters) > 0 {
		name += "By"
		name += strings.Join(filters, "And")
	}

	return name
}

// Return an expression evaluating if a filter should be used (based on active
// criteria).
func activeCriteria(filter []string) string {
	expr := ""
	for i, name := range filter {
		if i > 0 {
			expr += " && "
		}
		expr += fmt.Sprintf("criteria[%q] != nil", name)
	}

	return expr
}

// Return the transaction type name for the given database.
func dbTxType(db string) string {
	return fmt.Sprintf("*%sTx", lex.Capital(db))
}

// Return the code for a "dest" function, to be passed as parameter to
// query.SelectObjects in order to scan a single row.
func destFunc(slice string, typ string, fields []*Field) string {
	f := fmt.Sprintf(`func(i int) []interface{} {
                      %s = append(%s, %s{})
                      return []interface{}{
`, slice, slice, typ)

	for _, field := range fields {
		f += fmt.Sprintf("&%s[i].%s,\n", slice, field.Name)
	}

	f += "        }\n"
	f += "}"

	return f
}

// Return an index type of the form "map[string]map[string]...<typ>", with one
// level of indexing for each given field.
func indexType(fields []*Field, typ string) string {
	index := ""
	for range fields {
		index += "map[string]"
	}
	index += typ
	return index
}
