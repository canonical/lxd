package lex_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lxc/lxd/lxd/db/generate/lex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	pkg, err := lex.Parse(filepath.Dir(filename))
	require.NoError(t, err)

	obj := pkg.Scope.Lookup("Parse")
	assert.NotNil(t, obj)
}
