package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/config"
)

// Loading a config Map initializes it with the given values.
func TestLoad(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Setter: failingSetter},
		"egg": {Type: config.Bool},
	}

	cases := []struct {
		title  string
		values map[string]string // Initial values
		result map[string]string // Expected values after loading
	}{
		{
			`plain load of regular key`,
			map[string]string{"foo": "hello"},
			map[string]string{"foo": "hello"},
		},
		{
			`key setter is ignored upon loading`,
			map[string]string{"bar": "hello"},
			map[string]string{"bar": "hello"},
		},
		{
			`bool true values are normalized`,
			map[string]string{"egg": "yes"},
			map[string]string{"egg": "true"},
		},
		{
			`multiple values are all loaded`,
			map[string]string{"foo": "x", "bar": "yuk", "egg": "1"},
			map[string]string{"foo": "x", "bar": "yuk", "egg": "true"},
		},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			m, err := config.Load(schema, c.values)
			require.NoError(t, err)

			for name, value := range c.result {
				rawValue, err := m.GetRaw(name)
				require.NoError(t, err)
				assert.Equal(t, value, rawValue)
			}
		})
	}
}

// If some keys fail to load, an ErrorList with the offending issues is
// returned.
func TestLoad_Error(t *testing.T) {
	var cases = []struct {
		title   string
		schema  config.Schema     // Test schema to use
		values  map[string]string // Initial values
		message string            // Expected error message
	}{
		{
			`schema has no key with the given name`,
			config.Schema{},
			map[string]string{"bar": ""},
			"Cannot set 'bar' to '': Unknown key",
		},
		{
			`validation fails`,
			config.Schema{"foo": {Type: config.Bool}},
			map[string]string{"foo": "yyy"},
			"Cannot set 'foo' to 'yyy': Invalid boolean",
		},
		{
			`only the first of multiple errors is shown (in key name order)`,
			config.Schema{"foo": {Type: config.Bool}},
			map[string]string{"foo": "yyy", "bar": ""},
			"Cannot set 'bar' to '': Unknown key (and 1 more errors)",
		},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			_, err := config.Load(c.schema, c.values)
			assert.EqualError(t, err, c.message)
		})
	}
}

// Changing a config Map mutates the initial values.
func TestChange(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Setter: upperCase},
		"egg": {Type: config.Bool},
		"yuk": {Type: config.Bool, Default: "true"},
	}

	values := map[string]string{ // Initial values
		"foo": "hello",
		"bar": "x",
	}

	cases := []struct {
		title  string
		values map[string]string // New values
		result map[string]string // Expected values after change
	}{
		{
			`plain change of regular key`,
			map[string]string{"foo": "world"},
			map[string]string{"foo": "world"},
		},
		{
			`key setter is honored`,
			map[string]string{"bar": "y"},
			map[string]string{"bar": "Y"},
		},
		{
			`bool true values are normalized`,
			map[string]string{"egg": "yes"},
			map[string]string{"egg": "true"},
		},
		{
			`bool false values are normalized`,
			map[string]string{"yuk": "0"},
			map[string]string{"yuk": "false"},
		},
		{
			`multiple values are all mutated`,
			map[string]string{"foo": "x", "bar": "hey", "egg": "0"},
			map[string]string{"foo": "x", "bar": "HEY", "egg": ""},
		},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			m, err := config.Load(schema, values)
			require.NoError(t, err)

			_, err = m.Change(c.values)
			require.NoError(t, err)

			for name, value := range c.result {
				rawValue, err := m.GetRaw(name)
				require.NoError(t, err)
				assert.Equal(t, value, rawValue)
			}
		})
	}
}

// A map of changed key/value pairs is returned.
func TestMap_ChangeReturnsChangedKeys(t *testing.T) {
	schema := config.Schema{
		"foo": {Type: config.Bool},
		"bar": {Default: "egg"},
	}

	values := map[string]string{"foo": "true"} // Initial values

	cases := []struct {
		title   string
		changes map[string]string // New values
		changed map[string]string // Keys that should have actually changed
	}{
		{
			`plain single change`,
			map[string]string{"foo": "no"},
			map[string]string{"foo": "false"},
		},
		{
			`unchanged boolean value, even if it's spelled 'yes' and not 'true'`,
			map[string]string{"foo": "yes"},
			map[string]string{},
		},
		{
			`unset value`,
			map[string]string{"foo": ""},
			map[string]string{"foo": "false"},
		},
		{
			`unchanged value, since it matches the default`,
			map[string]string{"foo": "true", "bar": "egg"},
			map[string]string{},
		},
		{
			`multiple changes`,
			map[string]string{"foo": "false", "bar": "baz"},
			map[string]string{"foo": "false", "bar": "baz"},
		},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			m, err := config.Load(schema, values)
			assert.NoError(t, err)

			changed, err := m.Change(c.changes)
			require.NoError(t, err)
			assert.Equal(t, c.changed, changed)
		})
	}
}

// If some keys fail to load, an ErrorList with the offending issues is
// returned.
func TestMap_ChangeError(t *testing.T) {
	schema := config.Schema{
		"foo": {Type: config.Bool},
		"egg": {Setter: failingSetter},
	}

	var cases = []struct {
		title   string
		changes map[string]string
		message string
	}{
		{
			`schema has no key with the given name`,
			map[string]string{"xxx": ""},
			"Cannot set 'xxx' to '': Unknown key",
		},
		{
			`validation fails`,
			map[string]string{"foo": "yyy"},
			"Cannot set 'foo' to 'yyy': Invalid boolean",
		},
		{
			`custom setter fails`,
			map[string]string{"egg": "xxx"},
			"Cannot set 'egg' to 'xxx': boom",
		},
	}

	for _, c := range cases {
		t.Run(c.message, func(t *testing.T) {
			m, err := config.Load(schema, nil)
			assert.NoError(t, err)

			_, err = m.Change(c.changes)
			assert.EqualError(t, err, c.message)
		})
	}
}

// A Map dump contains only values that differ from their default.
func TestMap_Dump(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
	}

	values := map[string]string{
		"foo": "hello",
		"bar": "x",
	}

	m, err := config.Load(schema, values)
	assert.NoError(t, err)

	dumpExpected := map[string]string{
		"foo": "hello",
	}

	dump, err := m.Dump()
	assert.NoError(t, err)
	assert.Equal(t, dumpExpected, dump)
}

// The various GetXXX methods return typed values.
func TestMap_Getters(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Type: config.Bool},
		"egg": {Type: config.Int64},
	}

	values := map[string]string{
		"foo": "hello",
		"bar": "true",
		"egg": "123",
	}

	m, err := config.Load(schema, values)
	assert.NoError(t, err)

	s, err := m.GetString("foo")
	assert.NoError(t, err)
	assert.Equal(t, "hello", s)

	b, err := m.GetBool("bar")
	assert.NoError(t, err)
	assert.Equal(t, true, b)

	i, err := m.GetInt64("egg")
	assert.NoError(t, err)
	assert.Equal(t, int64(123), i)
}

// The various GetXXX methods return an error if they are used with the wrong key name or type.
func TestMap_GettersReturnError(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Type: config.Bool},
	}

	m, err := config.Load(schema, nil)
	assert.NoError(t, err)

	r, err := m.GetRaw("egg")
	assert.Error(t, err)
	assert.Empty(t, r)

	s, err := m.GetString("bar")
	assert.Error(t, err)
	assert.Empty(t, s)

	b, err := m.GetBool("foo")
	assert.Error(t, err)
	assert.False(t, b)

	i, err := m.GetInt64("foo")
	assert.Error(t, err)
	assert.Zero(t, i)
}

// A Key setter that always fail.
func failingSetter(string) (string, error) {
	return "", fmt.Errorf("boom")
}

// A Key setter that uppercases the value.
func upperCase(v string) (string, error) {
	return strings.ToUpper(v), nil
}
