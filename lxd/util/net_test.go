package util_test

import (
	"io"
	"testing"

	"github.com/lxc/lxd/lxd/util"
	"github.com/mpvl/subtest"
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
		subtest.Run(t, in, func(t *testing.T) {
			assert.Equal(t, out, util.CanonicalNetworkAddress(in))
		})
	}

}
