package util_test

import (
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/lxc/lxd/lxd/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The connection returned by the dialer is paired with the one returned by the
// Accept() method of the listener.
func TestInMemoryNetwork(t *testing.T) {
	listener, dialer := util.InMemoryNetwork()
	client := dialer()
	server, err := listener.Accept()
	require.NoError(t, err)

	go client.Write([]byte("hello"))
	buffer := make([]byte, 5)
	n, err := server.Read(buffer)
	require.NoError(t, err)

	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), buffer)

	// Closing the server makes all further client reads and
	// writes fail.
	server.Close()
	_, err = client.Read(buffer)
	assert.Equal(t, io.EOF, err)
	_, err = client.Write([]byte("hello"))
	assert.EqualError(t, err, "io: read/write on closed pipe")
}

func TestCanonicalNetworkAddress(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1":                             "127.0.0.1:8443",
		"foo.bar":                               "foo.bar:8443",
		"192.168.1.1:443":                       "192.168.1.1:443",
		"f921:7358:4510:3fce:ac2e:844:2a35:54e": "[f921:7358:4510:3fce:ac2e:844:2a35:54e]:8443",
	}
	for in, out := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, out, util.CanonicalNetworkAddress(in))
		})
	}

}

func TestIsAddressCovered(t *testing.T) {
	type testCase struct {
		address1 string
		address2 string
		covered  bool
	}

	cases := []testCase{
		{"127.0.0.1:8443", "127.0.0.1:8443", true},
		{"garbage", "127.0.0.1:8443", false},
		{"127.0.0.1:8444", "garbage", false},
		{"127.0.0.1:8444", "127.0.0.1:8443", false},
		{"127.0.0.1:8443", "0.0.0.0:8443", true},
		{"[::1]:8443", "0.0.0.0:8443", false},
		{":8443", "0.0.0.0:8443", false},
		{"127.0.0.1:8443", "[::]:8443", true},
		{"[::1]:8443", "[::]:8443", true},
		{"[::1]:8443", ":8443", true},
		{":8443", "[::]:8443", true},
		{"0.0.0.0:8443", "[::]:8443", true},
		{"10.30.0.8:8443", "[::]", true},
		{"localhost:8443", "127.0.0.1:8443", true},
	}

	// Test some localhost cases too
	ips, err := net.LookupHost("localhost")
	if err == nil && len(ips) > 0 && ips[0] == "127.0.0.1" {
		cases = append(cases, testCase{"127.0.0.1:8443", "localhost:8443", true})
	}

	ips, err = net.LookupHost("ip6-localhost")
	if err == nil && len(ips) > 0 && ips[0] == "::1" {
		cases = append(cases, testCase{"[::1]:8443", "ip6-localhost:8443", true})
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%s-%s", c.address1, c.address2), func(t *testing.T) {
			covered := util.IsAddressCovered(c.address1, c.address2)
			if c.covered {
				assert.True(t, covered)
			} else {
				assert.False(t, covered)
			}
		})
	}
}

// This is a check against Go's stdlib to make sure that when listening to a port without specifying an address,
// then an IPv6 wildcard is assumed.
func TestListenImplicitIPv6Wildcard(t *testing.T) {
	listener, err := net.Listen("tcp", ":9999")
	require.NoError(t, err)
	defer listener.Close()

	assert.Equal(t, "[::]:9999", listener.Addr().String())
}
