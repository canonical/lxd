package util_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/util"
	"github.com/mpvl/subtest"
	"github.com/stretchr/testify/assert"
)

func TestCanonicalNetworkAddress(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1":                             "127.0.0.1:8443",
		"192.168.1.1:443":                       "192.168.1.1:443",
		"f921:7358:4510:3fce:ac2e:844:2a35:54e": "[f921:7358:4510:3fce:ac2e:844:2a35:54e]:8443",
	}
	for in, out := range cases {
		subtest.Run(t, in, func(t *testing.T) {
			assert.Equal(t, out, util.CanonicalNetworkAddress(in))
		})
	}

}
