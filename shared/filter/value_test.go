package filter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/filter"
)

type innerStruct struct {
	Value string `yaml:"value"`
}

type outerStruct struct {
	Name   string      `yaml:"name"`
	Inline innerStruct `yaml:",inline"`
	Map    map[string]string
}

func TestValueOf_PointerKind(t *testing.T) {
	// Nil pointer
	var nilPtr *outerStruct
	got := filter.ValueOf(nilPtr, "name")
	if got != nil {
		t.Errorf("ValueOf(nilPtr, \"name\") = %v, want nil", got)
	}

	// Non-nil pointer
	ptr := &outerStruct{Name: "Alice"}
	got = filter.ValueOf(ptr, "name")
	if got != "Alice" {
		t.Errorf("ValueOf(ptr, \"name\") = %v, want \"Alice\"", got)
	}
}

func TestValueOf_MapStringString(t *testing.T) {
	m := map[string]string{"foo": "bar"}
	got := filter.ValueOf(m, "foo")
	if got != "bar" {
		t.Errorf("ValueOf(map, \"foo\") = %v, want \"bar\"", got)
	}
}

func TestValueOf_MapNested(t *testing.T) {
	nested := map[string]map[string]string{
		"outer": {"inner": "value"},
	}

	got := filter.ValueOf(nested, "outer.inner")
	if got != "value" {
		t.Errorf("ValueOf(nested, \"outer.inner\") = %v, want \"value\"", got)
	}
}

func TestValueOf_StructInline(t *testing.T) {
	obj := outerStruct{
		Name:   "Bob",
		Inline: innerStruct{Value: "baz"},
	}

	got := filter.ValueOf(obj, "value")
	if got != "baz" {
		t.Errorf("ValueOf(obj, \"value\") = %v, want \"baz\"", got)
	}
}

func TestValueOf_StructField(t *testing.T) {
	obj := outerStruct{Name: "Carol"}
	got := filter.ValueOf(obj, "name")
	if got != "Carol" {
		t.Errorf("ValueOf(obj, \"name\") = %v, want \"Carol\"", got)
	}
}

func TestValueOf_StructInlinePointer(t *testing.T) {
	obj := &outerStruct{
		Name:   "Dave",
		Inline: innerStruct{Value: "qux"},
	}

	got := filter.ValueOf(obj, "value")
	if got != "qux" {
		t.Errorf("ValueOf(obj, \"value\") = %v, want \"qux\"", got)
	}
}

func TestValueOf_UnknownField(t *testing.T) {
	obj := outerStruct{Name: "Eve"}
	got := filter.ValueOf(obj, "unknown")
	if got != nil {
		t.Errorf("ValueOf(obj, \"unknown\") = %v, want nil", got)
	}
}

func TestValueOf_MapKeyNotFound(t *testing.T) {
	m := map[string]string{"foo": "bar"}
	got := filter.ValueOf(m, "baz")
	if got != "" {
		t.Errorf("ValueOf(map, \"baz\") = %v, want \"\"", got)
	}
}

func TestValueOf_MapTypeMismatch(t *testing.T) {
	m := map[int]string{1: "one"}
	got := filter.ValueOf(m, "1")
	if got != nil {
		t.Errorf("ValueOf(map[int]string, \"1\") = %v, want nil", got)
	}
}

func TestValueOf_NonStructOrMap(t *testing.T) {
	val := 42
	got := filter.ValueOf(val, "anything")
	if got != nil {
		t.Errorf("ValueOf(42, \"anything\") = %v, want nil", got)
	}
}

func TestValueOf_Instance(t *testing.T) {
	date := time.Date(2020, 1, 29, 11, 10, 32, 0, time.UTC)
	instance := api.Instance{
		Name:         "c1",
		Status:       "Running",
		Architecture: "x86_64",
		Stateful:     false,
		CreatedAt:    date,
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

	cases := map[string]any{}
	cases["architecture"] = "x86_64"
	cases["created_at"] = date
	cases["config.image.os"] = "BusyBox"
	cases["name"] = "c1"
	cases["expanded_config.image.os"] = "BusyBox"
	cases["expanded_devices.root.pool"] = "default"
	cases["status"] = "Running"
	cases["stateful"] = false

	for field := range cases {
		t.Run(field, func(t *testing.T) {
			value := filter.ValueOf(instance, field)
			assert.Equal(t, cases[field], value)
		})
	}
}

func Benchmark_ValueOf_Instance(b *testing.B) {
	date := time.Date(2020, 1, 29, 11, 10, 32, 0, time.UTC)
	instance := api.Instance{
		Name:         "c1",
		Status:       "Running",
		Architecture: "x86_64",
		Stateful:     false,
		CreatedAt:    date,
	}

	for b.Loop() {
		_ = filter.ValueOf(instance, "architecture")
		_ = filter.ValueOf(instance, "created_at")
		_ = filter.ValueOf(instance, "name")
		_ = filter.ValueOf(instance, "status")
		_ = filter.ValueOf(instance, "stateful")
	}
}
