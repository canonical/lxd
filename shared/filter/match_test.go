package filter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/filter"
)

func TestMatch_Instance(t *testing.T) {
	instance := api.Instance{
		Name:         "c1",
		Status:       "Running",
		Architecture: "x86_64",
		Stateful:     false,
		CreatedAt:    time.Date(2020, 1, 29, 11, 10, 32, 0, time.UTC),
		Config: map[string]string{
			"image.os": "BusyBox",
		},
		ExpandedConfig: map[string]string{
			"image.os": "BusyBox",
		},
		ExpandedDevices: map[string]map[string]string{
			"root": {
				"path": "/",
				"pool": "default",
				"type": "disk",
			},
		},
	}

	cases := map[string]any{
		"architecture eq x86_64":                                         true,
		"architecture eq i686":                                           false,
		"name eq c1 and status eq Running":                               true,
		"config.image.os eq BusyBox and expanded_devices.root.path eq /": true,
		"name eq c2 or status eq Running":                                true,
		"name eq c2 or name eq c3":                                       false,
		"not name eq c2":                                                 true,
		"not name eq c1 or status eq Running":                            true,
		"name ne c2":                                                     true,
		"name ne c1":                                                     false,
		"architecture eq [":                                              false,
	}

	for s := range cases {
		t.Run(s, func(t *testing.T) {
			f, err := filter.Parse(s, filter.QueryOperatorSet())
			require.NoError(t, err)
			match, err := filter.Match(instance, *f)
			require.NoError(t, err)
			assert.Equal(t, cases[s], match)
		})
	}
}

func TestMatch_Image(t *testing.T) {
	image := api.Image{
		Public:       true,
		Architecture: "i686",
		Properties: map[string]string{
			"os": "Ubuntu",
		},
	}

	cases := map[string]any{
		"properties.os eq Ubuntu": true,
		"architecture eq x86_64":  false,
	}

	for s := range cases {
		t.Run(s, func(t *testing.T) {
			f, err := filter.Parse(s, filter.QueryOperatorSet())
			require.NoError(t, err)
			match, err := filter.Match(image, *f)
			require.NoError(t, err)
			assert.Equal(t, cases[s], match)
		})
	}
}

// matchTestObject is a helper type for testing Match with int, uint, bool, and slice field types.
type matchTestObject struct {
	Count  int64    `yaml:"count"`
	UCount uint64   `yaml:"ucount"`
	Active bool     `yaml:"active"`
	Tags   []string `yaml:"tags"`
	Counts []int    `yaml:"counts"`
	Score  float64  `yaml:"score"`
}

// extendedOperatorSet returns an OperatorSet that includes gt/lt/ge/le comparison operators.
func extendedOperatorSet() filter.OperatorSet {
	ops := filter.QueryOperatorSet()
	ops.GreaterThan = "gt"
	ops.LessThan = "lt"
	ops.GreaterEqual = "ge"
	ops.LessEqual = "le"
	return ops
}

func TestMatch_Bool(t *testing.T) {
	obj := matchTestObject{Active: true}

	cases := map[string]any{
		"active eq true":  true,
		"active eq false": false,
		"active ne false": true,
		"active ne true":  false,
	}

	for s := range cases {
		t.Run(s, func(t *testing.T) {
			f, err := filter.Parse(s, filter.QueryOperatorSet())
			require.NoError(t, err)
			match, err := filter.Match(obj, *f)
			require.NoError(t, err)
			assert.Equal(t, cases[s], match)
		})
	}
}

func TestMatch_Int(t *testing.T) {
	obj := matchTestObject{Count: 10}
	ops := extendedOperatorSet()

	cases := map[string]any{
		"count eq 10": true,
		"count eq 11": false,
		"count ne 11": true,
		"count ne 10": false,
		"count gt 9":  true,
		"count gt 10": false,
		"count lt 11": true,
		"count lt 10": false,
		"count ge 10": true,
		"count ge 11": false,
		"count le 10": true,
		"count le 9":  false,
	}

	for s := range cases {
		t.Run(s, func(t *testing.T) {
			f, err := filter.Parse(s, ops)
			require.NoError(t, err)
			match, err := filter.Match(obj, *f)
			require.NoError(t, err)
			assert.Equal(t, cases[s], match)
		})
	}
}

func TestMatch_Uint(t *testing.T) {
	obj := matchTestObject{UCount: 5}
	ops := extendedOperatorSet()

	cases := map[string]any{
		"ucount eq 5": true,
		"ucount eq 6": false,
		"ucount ne 6": true,
		"ucount ne 5": false,
		"ucount gt 4": true,
		"ucount gt 5": false,
		"ucount lt 6": true,
		"ucount lt 5": false,
		"ucount ge 5": true,
		"ucount ge 6": false,
		"ucount le 5": true,
		"ucount le 4": false,
	}

	for s := range cases {
		t.Run(s, func(t *testing.T) {
			f, err := filter.Parse(s, ops)
			require.NoError(t, err)
			match, err := filter.Match(obj, *f)
			require.NoError(t, err)
			assert.Equal(t, cases[s], match)
		})
	}
}

func TestMatch_StringSlice(t *testing.T) {
	obj := matchTestObject{Tags: []string{"a", "b"}}

	cases := map[string]any{
		`tags eq ["a","b"]`: true,
		`tags eq ["a","c"]`: false,
		`tags eq ["a"]`:     false,
		`tags ne ["a","b"]`: false,
		`tags ne ["a","c"]`: true,
	}

	for s := range cases {
		t.Run(s, func(t *testing.T) {
			f, err := filter.Parse(s, filter.QueryOperatorSet())
			require.NoError(t, err)
			match, err := filter.Match(obj, *f)
			require.NoError(t, err)
			assert.Equal(t, cases[s], match)
		})
	}
}

func TestMatch_Errors(t *testing.T) {
	t.Run("invalid float64 field type", func(t *testing.T) {
		obj := matchTestObject{Score: 1.5}
		f, err := filter.Parse("score eq 1.5", filter.QueryOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Invalid type")
	})

	t.Run("nonexistent field", func(t *testing.T) {
		obj := matchTestObject{}
		f, err := filter.Parse("missing eq foo", filter.QueryOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Invalid type")
	})

	t.Run("invalid slice element type", func(t *testing.T) {
		obj := matchTestObject{Counts: []int{1, 2}}
		f, err := filter.Parse("counts eq foo", filter.QueryOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Invalid slice type")
	})

	t.Run("failed parsing int value", func(t *testing.T) {
		obj := matchTestObject{Count: 10}
		f, err := filter.Parse("count eq notanumber", filter.QueryOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Failed parsing value")
	})

	t.Run("failed parsing uint value", func(t *testing.T) {
		obj := matchTestObject{UCount: 5}
		f, err := filter.Parse("ucount eq notanumber", filter.QueryOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Failed parsing value")
	})

	t.Run("failed parsing bool value", func(t *testing.T) {
		obj := matchTestObject{Active: true}
		f, err := filter.Parse("active eq notabool", filter.QueryOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Failed parsing value")
	})

	t.Run("failed parsing string slice value", func(t *testing.T) {
		obj := matchTestObject{Tags: []string{"a"}}
		f, err := filter.Parse("tags eq notjson", filter.QueryOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Failed parsing value")
	})

	t.Run("invalid operator GreaterThan for string field", func(t *testing.T) {
		obj := api.Instance{Name: "c1"}
		f, err := filter.Parse("name gt foo", extendedOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Invalid operator")
	})

	t.Run("invalid operator LessThan for bool field", func(t *testing.T) {
		obj := matchTestObject{Active: true}
		f, err := filter.Parse("active lt true", extendedOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Invalid operator")
	})

	t.Run("invalid operator GreaterEqual for string slice field", func(t *testing.T) {
		obj := matchTestObject{Tags: []string{"a"}}
		f, err := filter.Parse(`tags ge ["a"]`, extendedOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Invalid operator")
	})

	t.Run("invalid operator LessEqual for string field", func(t *testing.T) {
		obj := api.Instance{Name: "c1"}
		f, err := filter.Parse("name le foo", extendedOperatorSet())
		require.NoError(t, err)
		_, err = filter.Match(obj, *f)
		assert.ErrorContains(t, err, "Invalid operator")
	})

	t.Run("unsupported operation", func(t *testing.T) {
		obj := matchTestObject{Count: 10}
		clauseSet := filter.ClauseSet{
			Clauses: []filter.Clause{{
				PrevLogical: filter.QueryOperatorSet().And,
				Field:       "count",
				Operator:    "unknown",
				Value:       "10",
			}},
			Ops: filter.QueryOperatorSet(),
		}

		_, err := filter.Match(obj, clauseSet)
		require.EqualError(t, err, "Unsupported operation")
	})

	t.Run("unexpected clause logical operator", func(t *testing.T) {
		obj := api.Instance{Name: "c1"}
		clauseSet := filter.ClauseSet{
			Clauses: []filter.Clause{{
				PrevLogical: "invalid",
				Field:       "name",
				Operator:    filter.QueryOperatorSet().Equals,
				Value:       "c1",
			}},
			Ops: filter.QueryOperatorSet(),
		}

		_, err := filter.Match(obj, clauseSet)
		require.EqualError(t, err, "unexpected clause operator")
	})
}
