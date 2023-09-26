package filter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/filter"
)

// TestMatch_Instance tests the Match function with various filter queries on an instance.
func TestMatch_Instance(t *testing.T) {
	instance := api.Instance{
		InstancePut: api.InstancePut{
			Architecture: "x86_64",
			Config: map[string]string{
				"image.os": "BusyBox",
			},
			Stateful: false,
		},
		CreatedAt: time.Date(2020, 1, 29, 11, 10, 32, 0, time.UTC),
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

	cases := map[string]any{
		"architecture eq x86_64":                                         true,
		"architecture eq i686":                                           false,
		"name eq c1 and status eq Running":                               true,
		"config.image.os eq BusyBox and expanded_devices.root.path eq /": true,
		"name eq c2 or status eq Running":                                true,
		"name eq c2 or name eq c3":                                       false,
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

// TestMatch_Image tests the Match function with various filter queries on an image.
func TestMatch_Image(t *testing.T) {
	image := api.Image{
		ImagePut: api.ImagePut{
			Public: true,
			Properties: map[string]string{
				"os": "Ubuntu",
			},
		},
		Architecture: "i686",
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
