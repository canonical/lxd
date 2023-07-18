package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/config"
)

// TestSchema_Defaults verifies that the Defaults method correctly provides default values for a given schema.
func TestSchema_Defaults(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
	}

	values := map[string]any{"foo": "", "bar": "x"}
	assert.Equal(t, values, schema.Defaults())
}

// TestSchema_Keys validates that the Keys method accurately lists all keys present in a given schema.
func TestSchema_Keys(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
	}

	keys := []string{"bar", "foo"}
	assert.Equal(t, keys, schema.Keys())
}
