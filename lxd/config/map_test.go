package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lxc/lxd/lxd/config"
	"github.com/mpvl/subtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		subtest.Run(t, c.title, func(t *testing.T) {
			m, err := config.Load(schema, c.values)
			require.NoError(t, err)

			for name, value := range c.result {
				assert.Equal(t, value, m.GetRaw(name))
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
			"cannot set 'bar' to '': unknown key",
		},
		{
			`validation fails`,
			config.Schema{"foo": {Type: config.Bool}},
			map[string]string{"foo": "yyy"},
			"cannot set 'foo' to 'yyy': invalid boolean",
		},
		{
			`only the first of multiple errors is shown (in key name order)`,
			config.Schema{"foo": {Type: config.Bool}},
			map[string]string{"foo": "yyy", "bar": ""},
			"cannot set 'bar' to '': unknown key (and 1 more errors)",
		},
	}
	for _, c := range cases {
		subtest.Run(t, c.title, func(t *testing.T) {
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
		"xyz": {Hidden: true},
	}
	values := map[string]string{ // Initial values
		"foo": "hello",
		"bar": "x",
		"xyz": "sekret",
	}

	cases := []struct {
		title  string
		values map[string]interface{} // New values
		result map[string]string      // Expected values after change
	}{
		{
			`plain change of regular key`,
			map[string]interface{}{"foo": "world"},
			map[string]string{"foo": "world"},
		},
		{
			`key setter is honored`,
			map[string]interface{}{"bar": "y"},
			map[string]string{"bar": "Y"},
		},
		{
			`bool true values are normalized`,
			map[string]interface{}{"egg": "yes"},
			map[string]string{"egg": "true"},
		},
		{
			`bool false values are normalized`,
			map[string]interface{}{"yuk": "0"},
			map[string]string{"yuk": "false"},
		},
		{
			`the special value 'true' is a passthrough for hidden keys`,
			map[string]interface{}{"xyz": true},
			map[string]string{"xyz": "sekret"},
		},
		{
			`the special value nil is converted to empty string`,
			map[string]interface{}{"foo": nil},
			map[string]string{"foo": ""},
		},
		{
			`multiple values are all mutated`,
			map[string]interface{}{"foo": "x", "bar": "hey", "egg": "0"},
			map[string]string{"foo": "x", "bar": "HEY", "egg": ""},
		},
	}

	for _, c := range cases {
		subtest.Run(t, c.title, func(t *testing.T) {
			m, err := config.Load(schema, values)
			require.NoError(t, err)

			_, err = m.Change(c.values)
			require.NoError(t, err)

			for name, value := range c.result {
				assert.Equal(t, value, m.GetRaw(name))
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
		changes map[string]interface{} // New values
		changed map[string]string      // Keys that should have actually changed
	}{
		{
			`plain single change`,
			map[string]interface{}{"foo": "no"},
			map[string]string{"foo": "false"},
		},
		{
			`unchanged boolean value, even if it's spelled 'yes' and not 'true'`,
			map[string]interface{}{"foo": "yes"},
			map[string]string{},
		},
		{
			`unset value`,
			map[string]interface{}{"foo": ""},
			map[string]string{"foo": "false"},
		},
		{
			`unchanged value, since it matches the default`,
			map[string]interface{}{"foo": "true", "bar": "egg"},
			map[string]string{},
		},
		{
			`multiple changes`,
			map[string]interface{}{"foo": "false", "bar": "baz"},
			map[string]string{"foo": "false", "bar": "baz"},
		},
	}
	for _, c := range cases {
		subtest.Run(t, c.title, func(t *testing.T) {
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
		changes map[string]interface{}
		message string
	}{
		{
			`schema has no key with the given name`,
			map[string]interface{}{"xxx": ""},
			"cannot set 'xxx' to '': unknown key",
		},
		{
			`validation fails`,
			map[string]interface{}{"foo": "yyy"},
			"cannot set 'foo' to 'yyy': invalid boolean",
		},
		{
			`custom setter fails`,
			map[string]interface{}{"egg": "xxx"},
			"cannot set 'egg' to 'xxx': boom",
		},
		{
			`non string value`,
			map[string]interface{}{"egg": 123},
			"cannot set 'egg': invalid type int",
		},
	}

	for _, c := range cases {
		subtest.Run(t, c.message, func(t *testing.T) {
			m, err := config.Load(schema, nil)
			assert.NoError(t, err)

			_, err = m.Change(c.changes)
			assert.EqualError(t, err, c.message)
		})
	}
}

// A Map dump contains only values that differ from their default. Hidden
// values are obfuscated.
func TestMap_Dump(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Default: "x"},
		"egg": {Hidden: true},
	}
	values := map[string]string{
		"foo": "hello",
		"bar": "x",
		"egg": "123",
	}
	m, err := config.Load(schema, values)
	assert.NoError(t, err)

	dump := map[string]interface{}{
		"foo": "hello",
		"egg": true,
	}
	assert.Equal(t, dump, m.Dump())
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

	assert.Equal(t, "hello", m.GetString("foo"))
	assert.Equal(t, true, m.GetBool("bar"))
	assert.Equal(t, int64(123), m.GetInt64("egg"))
}

// The various GetXXX methods panic if they are used with the wrong key name or
// type.
func TestMap_GettersPanic(t *testing.T) {
	schema := config.Schema{
		"foo": {},
		"bar": {Type: config.Bool},
	}

	m, err := config.Load(schema, nil)
	assert.NoError(t, err)

	assert.Panics(t, func() { m.GetRaw("egg") })
	assert.Panics(t, func() { m.GetString("bar") })
	assert.Panics(t, func() { m.GetBool("foo") })
	assert.Panics(t, func() { m.GetInt64("foo") })
}

// A Key setter that always fail.
func failingSetter(string) (string, error) {
	return "", fmt.Errorf("boom")
}

// A Key setter that uppercases the value.
func upperCase(v string) (string, error) {
	return strings.ToUpper(v), nil
}
