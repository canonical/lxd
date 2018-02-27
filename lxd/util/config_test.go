package util_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/util"
	"github.com/mpvl/subtest"
	"github.com/stretchr/testify/assert"
)

func Test_CompareConfigsMismatch(t *testing.T) {
	cases := []struct {
		config1 map[string]string
		config2 map[string]string
		error   string
	}{
		{
			map[string]string{"foo": "bar"},
			map[string]string{"foo": "egg"},
			"different values for keys: foo",
		},
		{
			map[string]string{"foo": "bar"},
			map[string]string{"egg": "buz"},
			"different values for keys: egg, foo",
		},
	}
	for _, c := range cases {
		subtest.Run(t, c.error, func(t *testing.T) {
			err := util.CompareConfigs(c.config1, c.config2, nil)
			assert.EqualError(t, err, c.error)
		})
	}
}

func Test_CompareConfigs(t *testing.T) {
	config1 := map[string]string{"foo": "bar", "baz": "buz"}
	config2 := map[string]string{"foo": "egg", "baz": "buz"}
	err := util.CompareConfigs(config1, config2, []string{"foo"})
	assert.NoError(t, err)
}
