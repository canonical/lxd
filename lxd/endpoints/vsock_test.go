package endpoints

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMicroVMPeerListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.socket")

	inner, err := net.Listen("unix", path)
	require.NoError(t, err)

	listener := &microVMPeerListener{Listener: inner}
	defer func() { _ = listener.Close() }()

	client, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	conn, err := listener.Accept()
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	assert.Equal(t, "unix", conn.RemoteAddr().Network())
	assert.Equal(t, "microvm("+strconv.Itoa(os.Getpid())+")", conn.RemoteAddr().String())
}
