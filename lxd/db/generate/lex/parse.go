package lex

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/canonical/lxd/shared"
)

// Parse runs the Go parser against the given package directory.
func Parse(dir string) (*packages.Package, error) {
	if !shared.IsDir(dir) {
		return nil, fmt.Errorf("Package directory does not exist %q", dir)
	}

	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("Search source file: %w", err)
	}

	files := []*ast.File{}
	for _, path := range paths {
		// Skip test files.
		if strings.Contains(path, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("Parse Go source file %q", path)
		}

		files = append(files, file)
	}

	pkgs, err := packages.Load(&packages.Config{Fset: fset}, dir)
	if err != nil {
		return nil, err
	}

	if len(pkgs) != 1 {
		return nil, fmt.Errorf("More than one package parsed")
	}

	// Using the Mode flags on packages.Config to populate the fields
	// on the returned Package significantly slows down the load time,
	// so instead, we can populate the one we care about directly from
	// the files we already compiled.
	pkgs[0].Syntax = files

	return pkgs[0], nil
}
