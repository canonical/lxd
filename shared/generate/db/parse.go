package db

import (
	"fmt"
	"go/ast"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/lxc/lxd/shared/generate/lex"
	"github.com/pkg/errors"
)

// Packages returns the the AST packages in which to search for structs.
//
// By default it includes the lxd/db and shared/api packages.
func Packages() (map[string]*ast.Package, error) {
	packages := map[string]*ast.Package{}

	for _, name := range defaultPackages {
		pkg, err := lex.Parse(name)
		if err != nil {
			return nil, errors.Wrapf(err, "Parse %q", name)
		}
		parts := strings.Split(name, "/")
		packages[parts[len(parts)-1]] = pkg
	}

	return packages, nil
}

var defaultPackages = []string{
	"github.com/lxc/lxd/shared/api",
	"github.com/lxc/lxd/lxd/db",
}

// Filters parses all filtering statement defined for the given entity. It
// returns all supported combinations of filters, sorted by number of criteria.
func Filters(pkg *ast.Package, entity string) [][]string {
	objects := pkg.Scope.Objects
	filters := [][]string{}

	prefix := fmt.Sprintf("%sObjectsBy", entity)

	for name := range objects {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := name[len(prefix):]
		filters = append(filters, strings.Split(rest, "And"))
	}

	sort.SliceStable(filters, func(i, j int) bool {
		return len(filters[i]) > len(filters[j])
	})

	return filters
}

// RefFilters parses all filtering statement defined for the given entity reference.
func RefFilters(pkg *ast.Package, entity string, ref string) [][]string {
	objects := pkg.Scope.Objects
	filters := [][]string{}

	prefix := fmt.Sprintf("%s%sRefBy", entity, lex.Capital(ref))

	for name := range objects {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := name[len(prefix):]
		filters = append(filters, strings.Split(rest, "And"))
	}

	sort.SliceStable(filters, func(i, j int) bool {
		return len(filters[i]) > len(filters[j])
	})

	return filters
}

func Criteria(pkg *ast.Package, entity string) ([]string, error) {
	name := fmt.Sprintf("%sFilter", lex.Capital(entity))
	str := findStruct(pkg.Scope, name)

	if str == nil {
		return nil, fmt.Errorf("No filter declared for %q", entity)
	}

	criteria := []string{}

	for _, f := range str.Fields.List {
		if len(f.Names) != 1 {
			return nil, fmt.Errorf("Unexpected fields number")
		}

		if !f.Names[0].IsExported() {
			return nil, fmt.Errorf("Unexported field name")
		}

		criteria = append(criteria, f.Names[0].Name)
	}

	return criteria, nil
}

// Parse the structure declaration with the given name found in the given Go
// package.
func Parse(pkg *ast.Package, name string) (*Mapping, error) {
	str := findStruct(pkg.Scope, name)
	if str == nil {
		return nil, fmt.Errorf("No declaration found for %q", name)
	}

	fields, err := parseStruct(str)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to parse %q", name)
	}

	m := &Mapping{
		Package: pkg.Name,
		Name:    name,
		Fields:  fields,
	}

	return m, nil
}

// Find the StructType node for the structure with the given name
func findStruct(scope *ast.Scope, name string) *ast.StructType {
	obj := scope.Lookup(name)
	if obj == nil {
		return nil
	}

	typ, ok := obj.Decl.(*ast.TypeSpec)
	if !ok {
		return nil
	}

	str, ok := typ.Type.(*ast.StructType)
	if !ok {
		return nil
	}

	return str
}

// Extract field information from the given structure.
func parseStruct(str *ast.StructType) ([]*Field, error) {
	fields := make([]*Field, 0)

	for _, f := range str.Fields.List {
		if len(f.Names) == 0 {
			// Check if this is a parent struct.
			ident, ok := f.Type.(*ast.Ident)
			if !ok {
				continue
			}

			typ, ok := ident.Obj.Decl.(*ast.TypeSpec)
			if !ok {
				continue
			}

			parentStr, ok := typ.Type.(*ast.StructType)
			if !ok {
				continue
			}

			parentFields, err := parseStruct(parentStr)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed to parse parent struct")
			}
			fields = append(fields, parentFields...)

			continue
		}

		if len(f.Names) != 1 {
			return nil, fmt.Errorf("Expected a single field name, got %q", f.Names)
		}

		field, err := parseField(f)
		if err != nil {
			return nil, err
		}

		fields = append(fields, field)
	}

	return fields, nil
}

func parseField(f *ast.Field) (*Field, error) {
	name := f.Names[0]

	if !name.IsExported() {
		//return nil, fmt.Errorf("Unexported field name %q", name.Name)
	}

	typeName := parseType(f.Type)
	if typeName == "" {
		return nil, fmt.Errorf("Unsupported type for field %q", name.Name)
	}

	typeObj := Type{
		Name: typeName,
	}

	if IsColumnType(typeName) {
		typeObj.Code = TypeColumn
	} else if strings.HasPrefix(typeName, "[]") {
		typeObj.Code = TypeSlice
	} else if strings.HasPrefix(typeName, "map[") {
		typeObj.Code = TypeMap
	} else {
		return nil, fmt.Errorf("Unsupported type for field %q", name.Name)
	}

	var config url.Values
	if f.Tag != nil {
		tag := f.Tag.Value
		var err error
		config, err = url.ParseQuery(reflect.StructTag(tag[1 : len(tag)-1]).Get("db"))
		if err != nil {
			return nil, errors.Wrap(err, "Parse 'db' structure tag")
		}
	}

	field := Field{
		Name:   name.Name,
		Type:   typeObj,
		Config: config,
	}

	return &field, nil
}

func parseType(x ast.Expr) string {
	switch t := x.(type) {
	case *ast.StarExpr:
		// Pointers are not supported.
		return ""
	case *ast.SelectorExpr:
		return parseType(t.X) + "." + t.Sel.String()
	case *ast.Ident:
		s := t.String()
		if s == "byte" {
			return "uint8"
		}
		return s
	case *ast.ArrayType:
		return "[" + parseType(t.Len) + "]" + parseType(t.Elt)
	case *ast.MapType:
		return "map[" + parseType(t.Key) + "]" + parseType(t.Value)
	case *ast.BasicLit:
		return t.Value
	case nil:
		return ""
	default:
		return ""
	}
}

var simpleTypeNames = []string{
	"bool",
	"string",
	"int",
}
