package db_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/canonical/lxd/lxd/db/generate/db"
	"github.com/canonical/lxd/lxd/db/generate/lex"
)

func TestPackages(t *testing.T) {
	packages, err := db.Packages()
	require.NoError(t, err)

	assert.Len(t, packages, 2)

	pkg := packages["api"]
	assert.NotNil(t, pkg)

	objs := db.GetVars(pkg)
	assert.NotNil(t, objs)
	assert.NotNil(t, objs["StatusCodeNames"])
}

type Person struct {
	Name string
}

type Class struct {
	Time time.Time
	Room string
}

type Teacher struct {
	Person
	Subjects     []string
	IsSubstitute bool
	Classes      []Class
}

type TeacherFilter struct {
}

func TestGetVar(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	pkg, err := lex.Parse(filepath.Dir(filename))
	require.NoError(t, err)

	objs := db.GetVars(pkg)
	assert.NotNil(t, objs)
	assert.NotNil(t, objs["Imports"])
}

func TestParse(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(cwd, "parse_test.go"), nil, parser.ParseComments)
	require.NoError(t, err)

	files := []*ast.File{file}

	// Tests flag has to be set to parse test files.
	pkgs, err := packages.Load(&packages.Config{Tests: true, Fset: fset}, cwd)
	require.NoError(t, err)

	var pkg *packages.Package
	for _, p := range pkgs {
		if p.Name == "db_test" {
			pkg = p
			pkg.Syntax = files
		}
	}

	assert.NotNil(t, pkg)
	m, err := db.Parse(pkg, "Teacher", "objects")
	require.NoError(t, err)

	assert.Equal(t, "db_test", m.Package)
	assert.Equal(t, "Teacher", m.Name)

	fields := m.Fields

	assert.Len(t, fields, 4)

	assert.Equal(t, "Name", fields[0].Name)
	assert.Equal(t, "Subjects", fields[1].Name)
	assert.Equal(t, "IsSubstitute", fields[2].Name)
	assert.Equal(t, "Classes", fields[3].Name)

	assert.Equal(t, "string", fields[0].Type.Name)
	assert.Equal(t, "[]string", fields[1].Type.Name)
	assert.Equal(t, "bool", fields[2].Type.Name)
	assert.Equal(t, "[]Class", fields[3].Type.Name)

	assert.Equal(t, db.TypeColumn, fields[0].Type.Code)
	assert.Equal(t, db.TypeSlice, fields[1].Type.Code)
	assert.Equal(t, db.TypeColumn, fields[2].Type.Code)
	assert.Equal(t, db.TypeSlice, fields[3].Type.Code)
}
