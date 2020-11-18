package lex_test

import (
	"testing"

	"github.com/grant-he/lxd/shared/generate/lex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	pkg, err := lex.Parse("github.com/grant-he/lxd/shared/generate/lex")
	require.NoError(t, err)

	obj := pkg.Scope.Lookup("Parse")
	assert.NotNil(t, obj)
}
