package filter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/filter"
)

// TestValueOf_Instance tests the ValueOf function for specific fields in the instance struct.
func TestValueOf_Instance(t *testing.T) {
	date := time.Date(2020, 1, 29, 11, 10, 32, 0, time.UTC)
	instance := api.Instance{
		InstancePut: api.InstancePut{
			Architecture: "x86_64",
			Config: map[string]string{
				"image.os": "BusyBox",
			},
			Stateful: false,
		},
		CreatedAt: date,
		Name:      "c1",
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
		Status: "Running",
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
