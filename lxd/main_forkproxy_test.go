package main

import (
	"log"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAddr(t *testing.T) {
	tests := []struct {
		name       string
		address    string
		expected   *proxyAddress
		shouldFail bool
	}{
		// Port testing
		{
			"Single port",
			"tcp:127.0.0.1:2000",
			&proxyAddress{
				"tcp",
				[]string{"127.0.0.1:2000"},
				false,
			},
			false,
		},
		{
			"Multiple ports",
			"tcp:127.0.0.1:2000,2002",
			&proxyAddress{
				"tcp",
				[]string{
					"127.0.0.1:2000",
					"127.0.0.1:2002",
				},
				false,
			},
			false,
		},
		{
			"Port range",
			"tcp:127.0.0.1:2000-2002",
			&proxyAddress{
				"tcp",
				[]string{
					"127.0.0.1:2000",
					"127.0.0.1:2001",
					"127.0.0.1:2002",
				},
				false,
			},
			false,
		},
		{
			"Mixed ports and port ranges",
			"tcp:127.0.0.1:2000,2002,3000-3003,4000-4003",
			&proxyAddress{
				"tcp",
				[]string{
					"127.0.0.1:2000",
					"127.0.0.1:2002",
					"127.0.0.1:3000",
					"127.0.0.1:3001",
					"127.0.0.1:3002",
					"127.0.0.1:3003",
					"127.0.0.1:4000",
					"127.0.0.1:4001",
					"127.0.0.1:4002",
					"127.0.0.1:4003",
				},
				false,
			},
			false,
		},
		// connType testing
		{
			"UDP",
			"udp:127.0.0.1:2000",
			&proxyAddress{
				"udp",
				[]string{"127.0.0.1:2000"},
				false,
			},
			false,
		},
		{
			"Unix socket",
			"unix:/foobar",
			&proxyAddress{
				"unix",
				[]string{"/foobar"},
				false,
			},
			false,
		},
		{
			"Abstract unix socket",
			"unix:@/foobar",
			&proxyAddress{
				"unix",
				[]string{"@/foobar"},
				true,
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
			&proxyAddress{
				"tcp",
				[]string{"[fd39:2561:7238:91b5:0:0:0:0]:2000"},
				false,
			},
			false,
		},
		{
			"Valid IPv6 address (2)",
			"tcp:[fd39:2561:7238:91b5::0]:2000",
			&proxyAddress{
				"tcp",
				[]string{"[fd39:2561:7238:91b5::0]:2000"},
				false,
			},
			false,
		},
		{
			"Valid IPv6 address (3)",
			"tcp:[::1]:2000",
			&proxyAddress{
				"tcp",
				[]string{"[::1]:2000"},
				false,
			},
			false,
		},
		{
			"Valid IPv6 address (4)",
			"tcp:[::]:2000",
			&proxyAddress{
				"tcp",
				[]string{"[::]:2000"},
				false,
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
		addr, err := parseAddr(tt.address)
		if tt.shouldFail {
			require.Error(t, err)
			require.Nil(t, addr)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tt.expected, addr)
	}
}
