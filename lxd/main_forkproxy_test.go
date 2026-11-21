package main

import (
	"io"
	"log"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/network"
)

func TestParseAddr(t *testing.T) {
	tests := []struct {
		name       string
		address    string
		expected   *deviceConfig.ProxyAddress
		shouldFail bool
	}{
		// Port testing
		{
			"Single port",
			"tcp:127.0.0.1:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Multiple ports",
			"tcp:127.0.0.1:2000,2002",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports: []uint64{
					2000,
					2002,
				},
				Abstract: false,
			},
			false,
		},
		{
			"Port range",
			"tcp:127.0.0.1:2000-2002",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports: []uint64{
					2000,
					2001,
					2002,
				},
				Abstract: false,
			},
			false,
		},
		{
			"Mixed ports and port ranges",
			"tcp:127.0.0.1:2000,2002,3000-3003,4000-4003",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "127.0.0.1",
				Ports: []uint64{
					2000,
					2002,
					3000,
					3001,
					3002,
					3003,
					4000,
					4001,
					4002,
					4003,
				},
				Abstract: false,
			},
			false,
		},
		// connType testing
		{
			"UDP",
			"udp:127.0.0.1:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "udp",
				Address:  "127.0.0.1",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Unix socket",
			"unix:/foobar",
			&deviceConfig.ProxyAddress{
				ConnType: "unix",
				Address:  "/foobar",
				Abstract: false,
			},
			false,
		},
		{
			"Abstract unix socket",
			"unix:@/foobar",
			&deviceConfig.ProxyAddress{
				ConnType: "unix",
				Address:  "@/foobar",
				Abstract: true,
			},
			false,
		},
		{
			"Unknown connection type",
			"bla:blub",
			nil,
			true,
		},
		// Address testing
		{
			"Valid IPv6 address (1)",
			"tcp:[fd39:2561:7238:91b5:0:0:0:0]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "fd39:2561:7238:91b5:0:0:0:0",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Valid IPv6 address (2)",
			"tcp:[fd39:2561:7238:91b5::0]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "fd39:2561:7238:91b5::0",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Valid IPv6 address (3)",
			"tcp:[::1]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "::1",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Valid IPv6 address (4)",
			"tcp:[::]:2000",
			&deviceConfig.ProxyAddress{
				ConnType: "tcp",
				Address:  "::",
				Ports:    []uint64{2000},
				Abstract: false,
			},
			false,
		},
		{
			"Invalid IPv6 address (1)",
			"tcp:fd39:2561:7238:91b5:0:0:0:0:2000",
			nil,
			true,
		},
		{
			"Invalid IPv6 address (2)",
			"tcp:fd39:2561:7238:91b5::0:2000",
			nil,
			true,
		},
	}

	for i, tt := range tests {
		log.Printf("Running test #%d: %s", i, tt.name)
		addr, err := network.ProxyParseAddr(tt.address)
		if tt.shouldFail {
			require.Error(t, err)
			require.Nil(t, addr)
			continue
		}

		require.NoError(t, err)
		require.Equal(t, tt.expected, addr)
	}
}

// TestGenericRelayTCPToUnixEOF checks that a client disconnecting from the TCP side of the relay is
// propagated to the unix socket target, and that the relay then closes both connections.
func TestGenericRelayTCPToUnixEOF(t *testing.T) {
	type readResult struct {
		data []byte
		err  error
	}

	socketPath := filepath.Join(t.TempDir(), "target.sock")

	// Target behind the unix socket which reads until EOF and never writes anything back.
	unixListener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer func() { _ = unixListener.Close() }()

	targetResult := make(chan readResult, 1)
	go func() {
		conn, err := unixListener.Accept()
		if err != nil {
			targetResult <- readResult{err: err}
			return
		}

		defer func() { _ = conn.Close() }()

		data, err := io.ReadAll(conn)
		targetResult <- readResult{data: data, err: err}
	}()

	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = tcpListener.Close() }()

	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)

		srcConn, err := tcpListener.Accept()
		if err != nil {
			return
		}

		dstConn, err := net.Dial("unix", socketPath)
		if err != nil {
			_ = srcConn.Close()
			return
		}

		genericRelay(srcConn, dstConn)
	}()

	client, err := net.Dial("tcp", tcpListener.Addr().String())
	require.NoError(t, err)

	_, err = client.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, client.Close())

	select {
	case res := <-targetResult:
		require.NoError(t, res.err)
		require.Equal(t, "hello", string(res.data))
	case <-time.After(5 * time.Second):
		t.Fatal("Unix socket target did not receive EOF after the TCP client disconnected")
	}

	select {
	case <-relayDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Relay did not finish after both directions completed")
	}
}
