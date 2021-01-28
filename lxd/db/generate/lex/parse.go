package lex

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// Parse runs the Go parser against the given package name.
func Parse(name string) (*ast.Package, error) {
	base := os.Getenv("GOPATH")
	if base == "" {
		base = "~/go"
	}
	dir := filepath.Join(base, "src", name)

	fset := token.NewFileSet()

	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, errors.Wrap(err, "Search source file")
	}

	files := map[string]*ast.File{}
	for _, path := range paths {
		// Skip test files.
		if strings.Contains(path, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("Parse Go source file %q", path)
		}

		files[path] = file
	}

	// Ignore errors because they are typically about unresolved symbols.
	pkg, _ := ast.NewPackage(fset, files, nil, nil)

	return pkg, nil
}
