package filter_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/filter"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
)

func TestValueOf_Instance(t *testing.T) {
	date := time.Date(2020, 1, 29, 11, 10, 32, 0, time.UTC)
	instance := api.Instance{
		InstancePut: api.InstancePut{
			Architecture: "x86_64",
			Config: map[string]string{
				"image.os": "Busybox",
			},
			Stateful: false,
		},
		CreatedAt: date,
		Name:      "c1",
		ExpandedConfig: map[string]string{
			"image.os": "Busybox",
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
	cases := map[string]interface{}{
		"architecture":               "x86_64",
		"created_at":                 date,
		"config.image.os":            "Busybox",
		"name":                       "c1",
		"expanded_config.image.os":   "Busybox",
		"expanded_devices.root.pool": "default",
		"status":                     "Running",
		"stateful":                   false,
	}
	for field := range cases {
		t.Run(field, func(t *testing.T) {
			value := filter.ValueOf(instance, field)
			assert.Equal(t, cases[field], value)
		})
	}

}
