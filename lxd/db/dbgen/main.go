// Command dbgen inspects tagged structs and generates interface implementations for them
// such that they can be used easily with generic functions defined in lxd/db/query/generic.go.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/fvbommel/sortorder"
)

const (
	// commentModel is what dbgen looks for to determine whether it should attempt to model the struct.
	// It is only valid to write this comment above a struct type definition.
	// The comment should be followed by a single string containing the name of the table that the struct models.
	// 	e.g. // db:model auth_groups
	commentModel = "// db:model "

	// commentModel is what dbgen looks for to determine if the field is not a native column of the modelled table.
	// It saves all the following text in the comment to use as a join clause during a select.
	// There can only be one join clause per field.
	// 	e.g. // db:join JOIN projects ON instances.project_id = projects.id
	commentJoin = "// db:join "

	// commentOmit can be used to omit a field from INSERT or UPDATE statements. This is useful for fields that have
	// default values set by the database or that do not change over time. (e.g. a creation date).
	// The comment is followed by one or more space-separated values that must be one of omitCreate, or omitUpdate.
	// 	E.g. // db:omit create update
	commentOmit = "// db:omit "

	// omitCreate is used in conjunction with commentOmit to omit a column from INSERT statements.
	omitCreate = "create"

	// omitUpdate is used in conjunction with commentOmit to omit a column from UPDATE statements.
	omitUpdate = "update"

	// tagDB is the tag that is used to describe the column that a field represents.
	// E.g. Given a table:
	// 	```sql
	// 	CREATE TABLE books (
	// 	   id INTEGER NOT NULL AUTOINCREMENT PRIMARY KEY,
	// 	   title TEXT NOT NULL,
	// 	   UNIQUE (title)
	// 	);
	// 	```
	// A struct can be created as:
	// 	```go
	// 	// BooksRow represents a row of the books table.
	// 	// db:model books
	// 	type BooksRow struct {
	// 	    ID int64     `db:"id"`
	// 	    Title string `db:"title"
	// 	}
	// 	```
	tagDB = "db"

	// tagSupplementalPrimary can be added to the `db` tag to indicate that the row modelled by a particular field is
	// the primary key for the table (or is one of a set forming a composite primary key).
	// If no primary keys are manually specified, `dbgen` assumes that the `id` column is the primary key.
	// E.g.
	// 	```sql
	// 	CREATE TABLE books_authors (
	// 	    book_id INTEGER NOT NULL,
	// 	    author_id INTEGER NOT NULL,
	// 	    FOREIGN KEY book_id REFERENCES books (id) ON DELETE CASCADE,
	// 	    FOREIGN KEY author_id REFERENCES authors (id) ON DELETE CASCADE,
	// 	    PRIMARY KEY (author_id, book_id)
	// 	) WITHOUT ROWID;
	// 	```
	// A struct can be created as:
	// 	```go
	// 	// BooksAuthorsRow represents a row of the books_authors table.
	// 	// db:model books_authors
	// 	type BooksAuthorsRow struct {
	//	    BookID int64 `db:"book_id,primary"
	//	    AuthorID int64 `db:"author_id,primary"
	// 	}
	// 	```
	tagSupplementalPrimary = "primary"

	// columnID denotes the "special" id column. It is special because by convention, the "id" column is typically an
	// auto-incrementing integer primary key. If no columns are manually marked as the primary key via
	// tagSupplementalPrimary, `dbgen` will assume that the "id" column is the primary key (or will error if no id
	// column is present). `dbgen` will never include the "id" column in "INSERT" or "UPDATE" statements (e.g. it is not
	// necessary to use commentOmit to exclude it).
	columnID = "id"
)

// unqualifiedColumnNameRegex matches a column name, without a prefixed `<table>.` qualifier.
// It is used to check for shorthand column name labels. If a label matches this regular expression, it is
// prepended with `<table>.` for generated select statements. Any other tag that does not match this expression is
// used directly.
var unqualifiedColumnNameRegex = regexp.MustCompile(`^[_a-z]+$`)

func main() {
	err := run()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("Package directory must be provided")
	}

	if len(os.Args) < 3 {
		return errors.New("Output file name must be provided")
	}

	if len(os.Args) > 3 {
		return errors.New("Unknown additional arguments")
	}

	// Directory to inspect.
	packageDirectory := os.Args[1]
	info, err := os.Stat(packageDirectory)
	if err != nil {
		return fmt.Errorf("Cannot stat package directory: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", packageDirectory)
	}

	// File to output relative to the package directory.
	outputFileName := os.Args[2]

	packageName, packageSpecs, err := getPackageSpecs(packageDirectory)
	if err != nil {
		return err
	}

	return writeOutputFile(packageName, packageSpecs, filepath.Join(packageDirectory, outputFileName))
}

// writeOutputFile executes all the spec templates and writes them to the output file path.
func writeOutputFile(packageName string, packageSpecs []Spec, outputFilePath string) error {
	var b bytes.Buffer

	// Write file header.
	b.WriteString("//go:build linux && cgo && !agent\n\n")
	b.WriteString("package " + packageName + "\n\n")
	b.WriteString("// Generated by dbgen - DO NOT EDIT\n")

	// Sort the specs so there is no churn on the generated file.
	slices.SortFunc(packageSpecs, func(a, b Spec) int {
		if sortorder.NaturalLess(a.StructName, b.StructName) {
			return -1
		}

		return 1
	})

	// Execute the template for each spec and write to the buffer.
	for _, spec := range packageSpecs {
		err := specSelectTemplate.Execute(&b, spec.templateContext())
		if err != nil {
			return err
		}

		// If the spec contains any joins we can't use it for any mutations, so skip the exec template.
		if len(spec.Joins) > 0 {
			continue
		}

		err = specExecTemplate.Execute(&b, spec.templateContext())
		if err != nil {
			return err
		}
	}

	// Create and write to file.
	outFile, err := os.Create(outputFilePath)
	if err != nil {
		return err
	}

	defer outFile.Close()
	_, err = b.WriteTo(outFile)
	if err != nil {
		return err
	}

	return nil
}

// getPackageSpecs parses each .go file in the package and creates a spec for each tagged struct.
func getPackageSpecs(packageDirectory string) (string, []Spec, error) {
	paths, err := filepath.Glob(filepath.Join(packageDirectory, "*.go"))
	if err != nil {
		return "", nil, fmt.Errorf("Failed listing package files: %w", err)
	}

	var allSpecs []Spec
	var pkgName string
	deferredDecls := make(map[*ast.StructType]*Spec)
	for _, path := range paths {
		// Parse file AST.
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return "", nil, fmt.Errorf("Failed parsing Go source file %q: %w", path, err)
		}

		// Set the package name if not already set.
		if pkgName == "" {
			pkgName = file.Name.Name
		}

		fileSpecs, deferredSpecs, err := getFileSpecs(file)
		if err != nil {
			return "", nil, err
		}

		maps.Copy(deferredDecls, deferredSpecs)
		allSpecs = append(allSpecs, fileSpecs...)
	}

	allSpecs, err = getDeferredSpecs(allSpecs, deferredDecls)
	if err != nil {
		return "", nil, err
	}

	return pkgName, allSpecs, nil
}

// getFileSpecs returns a Spec for each tagged struct in the file.
func getFileSpecs(f *ast.File) ([]Spec, map[*ast.StructType]*Spec, error) {
	deferredDecls := make(map[*ast.StructType]*Spec, len(f.Decls))
	specs := make([]Spec, 0, len(f.Decls))

	for _, decl := range f.Decls {
		// Skip any declarations that:
		// - Are are not type declarations.
		// - Don't have comments.
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE || genDecl.Doc == nil || len(genDecl.Doc.List) == 0 || len(genDecl.Specs) != 1 {
			continue
		}

		// The table name is defined by the db:model tag (a bit like swagger).
		tableName, ok := strings.CutPrefix(genDecl.Doc.List[len(genDecl.Doc.List)-1].Text, commentModel)
		if !ok {
			continue
		}

		newSpec := &Spec{
			TableName: tableName,
		}

		// At this point we've found a "db:model" comment, so we expect to find a struct type definition.
		typeSpec, ok := genDecl.Specs[0].(*ast.TypeSpec)
		if !ok {
			return nil, nil, fmt.Errorf("Encountered unexpected type spec %q", genDecl.Specs[0])
		}

		// Set the struct name. This is used in the receiver of the generated methods.
		newSpec.StructName = typeSpec.Name.Name

		structTypeSpec, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return nil, nil, fmt.Errorf("Encountered unexpected struct type spec %q", genDecl.Specs[0])
		}

		// Iterate over the struct fields.
		var deferred bool
		for _, field := range structTypeSpec.Fields.List {
			if field.Tag == nil {
				// If the field does not have a tag, it is referencing another model.

				// For now, only allow one referenced struct per model.
				if deferred {
					return nil, nil, fmt.Errorf("More than one referenced field for struct %q", newSpec.StructName)
				}

				// For now, only allow referenced structs to appear first in the outer struct.
				if len(newSpec.Fields) > 0 {
					return nil, nil, fmt.Errorf("Referenced field in struct %q must appear first", newSpec.StructName)
				}

				// The field is a referenced model without a tag.
				// We continue parsing remaining fields, but defer parsing of the referenced type until later
				// because we need to get it from the spec list and it may not have been parsed yet.
				deferredDecls[structTypeSpec] = newSpec
				deferred = true
				continue
			}

			// Set the field name. This is used to map database columns to struct values.
			newFieldSpec := FieldSpec{
				FieldName: field.Names[0].Name,
			}

			// Tags contain column names and supplemental data.
			tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get(tagDB)
			if tag == "" {
				continue
			}

			var supplemental string
			newFieldSpec.ColumnName, supplemental, ok = strings.Cut(tag, ",")
			if ok {
				// Only "primary" is currently supported as supplemental tag data.
				if supplemental != tagSupplementalPrimary {
					return nil, nil, fmt.Errorf("Invalid supplemental tag info %q for field %q in struct %q", supplemental, newFieldSpec.FieldName, newSpec.StructName)
				}

				newFieldSpec.Primary = true
			}

			// Check if it is only the column name (without the table name as a qualifier)
			// If it is only the column name, prepend the table name so that it is fully qualified.
			if unqualifiedColumnNameRegex.MatchString(newFieldSpec.ColumnName) {
				newFieldSpec.ColumnName = tableName + "." + newFieldSpec.ColumnName
			}

			// Check the field comment. Use "db:join" to specify joins required to get the value.
			if field.Doc != nil {
				for _, l := range field.Doc.List {
					join, ok := strings.CutPrefix(l.Text, commentJoin)
					if ok {
						if strings.HasSuffix(newSpec.StructName, "Row") {
							return nil, nil, fmt.Errorf("Invalid spec for struct %q: Structs with a `Row` suffix may not include joins", newSpec.StructName)
						}

						newSpec.Joins = append(newSpec.Joins, join)
					}

					omit, ok := strings.CutPrefix(l.Text, commentOmit)
					if ok {
						omits := strings.Fields(omit)
						if slices.Contains(omits, omitCreate) {
							newFieldSpec.SkipCreate = true
						}

						if slices.Contains(omits, omitUpdate) {
							newFieldSpec.SkipUpdate = true
						}
					}
				}
			}

			newSpec.Fields = append(newSpec.Fields, newFieldSpec)
		}

		// If not deferred, validate and append to spec list.
		if !deferred {
			if !newSpec.hasPrimaryKey() {
				var hasPrimary bool
				for i, f := range newSpec.Fields {
					unqualifiedColumnName, ok := newSpec.unqualifiedColumnName(f)
					if !ok {
						continue
					}

					if unqualifiedColumnName == columnID {
						newSpec.Fields[i].Primary = true
						hasPrimary = true
						break
					}
				}

				if !hasPrimary {
					return nil, nil, fmt.Errorf("Failed finding a primary key for %q", newSpec.StructName)
				}
			}

			if len(newSpec.Fields) == 0 {
				return nil, nil, errors.New("Table spec must have at least one field")
			}

			specs = append(specs, *newSpec)
		}
	}

	return specs, deferredDecls, nil
}

func getDeferredSpecs(allSpecs []Spec, deferredDecls map[*ast.StructType]*Spec) ([]Spec, error) {
	for structTypeSpec, newSpec := range deferredDecls {
		if newSpec.hasPrimaryKey() {
			return nil, fmt.Errorf("Struct %q contains both a referenced type and a primary key", newSpec.StructName)
		}

		// The first field in the list is the referenced model.
		// This is enforced when parsing file specs.
		fieldName := structTypeSpec.Fields.List[0].Names[0].Name

		// Get the type of the referenced field.
		ident, ok := structTypeSpec.Fields.List[0].Type.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("Struct %q contains an untagged field whose type is not a struct", newSpec.StructName)
		}

		ts, ok := ident.Obj.Decl.(*ast.TypeSpec)
		if !ok {
			return nil, fmt.Errorf("Struct %q contains an untagged field whose type is not a struct", newSpec.StructName)
		}

		// The referenced field must be a struct (because it is another modelled struct).
		_, ok = ts.Type.(*ast.StructType)
		if !ok {
			return nil, fmt.Errorf("Struct %q contains an untagged field whose type is not a struct", newSpec.StructName)
		}

		// Find the spec with the same struct name as the referenced type.
		idx := slices.IndexFunc(allSpecs, func(spec Spec) bool {
			return spec.StructName == ident.Name
		})

		if idx == -1 {
			return nil, fmt.Errorf("Modelled type %q contains struct %q which is not modelled", newSpec.StructName, ident.Name)
		}

		spec := allSpecs[idx]

		// Both specs must model the same table.
		if spec.TableName != newSpec.TableName {
			return nil, fmt.Errorf("Modelled type %q contains struct %q which models a different table", newSpec.StructName, ident.Name)
		}

		// Prepend fields from the referenced struct. The referenced struct is enforced to be the first field when parsing file specs.
		newSpec.ReferenceFieldName = fieldName
		newSpec.Reference = &spec
		allSpecs = append(allSpecs, *newSpec)
		delete(deferredDecls, structTypeSpec)
	}

	if len(deferredDecls) > 0 {
		return nil, errors.New("Referenced models may not reference other models")
	}

	return allSpecs, nil
}
