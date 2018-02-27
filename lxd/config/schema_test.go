package config_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/config"
	"github.com/stretchr/testify/assert"
)

func TestSchema_Defaults(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
	}
	values := map[string]interface{}{"foo": "", "bar": "x"}
	assert.Equal(t, values, schema.Defaults())
}

func TestSchema_Keys(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
	}
	keys := []string{"bar", "foo"}
	assert.Equal(t, keys, schema.Keys())
}
