package shared

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Example_parseIPRange() {
	_, allowedv4NetworkA, _ := net.ParseCIDR("192.168.1.0/24")
	_, allowedv4NetworkB, _ := net.ParseCIDR("192.168.0.0/16")
	_, allowedv6NetworkA, _ := net.ParseCIDR("fd22:c952:653e:3df6::/64")
	_, allowedv6NetworkB, _ := net.ParseCIDR("fd22:c952:653e::/48")

	ipRanges := []string{
		// Ranges within allowedv4NetworkA.
		"192.168.1.1-192.168.1.255",
		"0.0.0.1-192.168.1.255",
		"0.0.0.1-0.0.0.255",
		// Ranges outsde of allowedv4NetworkA but within allowedv4NetworkB.
		"192.168.0.1-192.168.0.255",
		"192.168.0.0-192.168.0.0",
		"0.0.2.0-0.0.2.255",
		// Invalid IP ranges.
		"0.0.0.0.1-192.168.1.255",
		"192.0.0.1-192.0.0.255",
		"0.0.0.1-1.0.0.255",
		"0.0.2.1-0.0.0.255",
		// Ranges within allowedv6NetworkA.
		"fd22:c952:653e:3df6::1-fd22:c952:653e:3df6::FFFF",
		"::1-::FFFF",
		// Ranges outsde of allowedv6NetworkA but within allowedv6NetworkB.
		"fd22:c952:653e:FFFF::1-fd22:c952:653e:FFFF::FFFF",
		"::AAAA:FFFF:FFFF:FFFF:1-::AAAA:FFFF:FFFF:FFFF:FFFF",
	}

	fmt.Println("With allowed networks")
	for _, ipRange := range ipRanges {
		parsedRange, err := ParseIPRange(ipRange, allowedv4NetworkA, allowedv4NetworkB, allowedv6NetworkA, allowedv6NetworkB)
		if err != nil {
			fmt.Printf("Err: %v\n", err)
			continue
		}

		fmt.Printf("Start: %s, End: %s\n", parsedRange.Start.String(), parsedRange.End.String())
	}

	fmt.Println("Without allowed networks")
	for _, ipRange := range ipRanges {
		parsedRange, err := ParseIPRange(ipRange)
		if err != nil {
			fmt.Printf("Err: %v\n", err)
			continue
		}

		fmt.Printf("Start: %s, End: %s\n", parsedRange.Start.String(), parsedRange.End.String())
	}

	// Output: With allowed networks
	// Start: 192.168.1.1, End: 192.168.1.255
	// Start: 192.168.1.1, End: 192.168.1.255
	// Start: 192.168.1.1, End: 192.168.1.255
	// Start: 192.168.0.1, End: 192.168.0.255
	// Start: 192.168.0.0, End: 192.168.0.0
	// Start: 192.168.2.0, End: 192.168.2.255
	// Err: Start IP "0.0.0.0.1" is invalid
	// Err: IP range "192.0.0.1-192.0.0.255" does not fall within any of the allowed networks [192.168.1.0/24 192.168.0.0/16 fd22:c952:653e:3df6::/64 fd22:c952:653e::/48]
	// Err: IP range "0.0.0.1-1.0.0.255" does not fall within any of the allowed networks [192.168.1.0/24 192.168.0.0/16 fd22:c952:653e:3df6::/64 fd22:c952:653e::/48]
	// Err: Start IP "0.0.2.1" must be less than End IP "0.0.0.255"
	// Start: fd22:c952:653e:3df6::1, End: fd22:c952:653e:3df6::ffff
	// Start: fd22:c952:653e:3df6::1, End: fd22:c952:653e:3df6::ffff
	// Start: fd22:c952:653e:ffff::1, End: fd22:c952:653e:ffff::ffff
	// Start: fd22:c952:653e:aaaa:ffff:ffff:ffff:1, End: fd22:c952:653e:aaaa:ffff:ffff:ffff:ffff
	// Without allowed networks
	// Start: 192.168.1.1, End: 192.168.1.255
	// Start: 0.0.0.1, End: 192.168.1.255
	// Start: 0.0.0.1, End: 0.0.0.255
	// Start: 192.168.0.1, End: 192.168.0.255
	// Start: 192.168.0.0, End: 192.168.0.0
	// Start: 0.0.2.0, End: 0.0.2.255
	// Err: Start IP "0.0.0.0.1" is invalid
	// Start: 192.0.0.1, End: 192.0.0.255
	// Start: 0.0.0.1, End: 1.0.0.255
	// Err: Start IP "0.0.2.1" must be less than End IP "0.0.0.255"
	// Start: fd22:c952:653e:3df6::1, End: fd22:c952:653e:3df6::ffff
	// Start: ::1, End: ::ffff
	// Start: fd22:c952:653e:ffff::1, End: fd22:c952:653e:ffff::ffff
	// Start: ::aaaa:ffff:ffff:ffff:1, End: ::aaaa:ffff:ffff:ffff:ffff
}

func Example_ipRangesOverlap() {
	rangePairs := [][2]string{
		{"10.1.1.1-10.1.1.2", "10.1.1.3-10.1.1.4"},
		{"10.1.1.1-10.1.2.1", "10.1.1.254-10.1.1.255"},
		{"10.1.1.1-10.1.1.6", "10.1.1.5-10.1.1.9"},
		{"10.1.1.5-10.1.1.9", "10.1.1.1-10.1.1.6"},
		{"::1-::2", "::3-::4"},
		{"::1-::6", "::5-::9"},
		{"::5-::9", "::1-::6"},
	}

	for _, pair := range rangePairs {
		r0, _ := ParseIPRange(pair[0])
		r1, _ := ParseIPRange(pair[1])
		result := r0.Overlaps(r1)
		fmt.Printf("Range1: %v, Range2: %v, overlapped: %t\n", r0, r1, result)
	}

	// also do a couple of tests with ranges that have no end
	singleIPRange := &IPRange{
		Start: net.ParseIP("10.1.1.4"),
	}

	otherRange, _ := ParseIPRange("10.1.1.1-10.1.1.6")

	fmt.Printf("Range1: %v, Range2: %v, overlapped: %t\n", singleIPRange, otherRange, singleIPRange.Overlaps(otherRange))
	fmt.Printf("Range1: %v, Range2: %v, overlapped: %t\n", otherRange, singleIPRange, otherRange.Overlaps(singleIPRange))
	fmt.Printf("Range1: %v, Range2: %v, overlapped: %t\n", singleIPRange, singleIPRange, singleIPRange.Overlaps(singleIPRange))

	otherRange, _ = ParseIPRange("10.1.1.8-10.1.1.9")

	fmt.Printf("Range1: %v, Range2: %v, overlapped: %t\n", singleIPRange, otherRange, singleIPRange.Overlaps(otherRange))
	fmt.Printf("Range1: %v, Range2: %v, overlapped: %t\n", otherRange, singleIPRange, otherRange.Overlaps(singleIPRange))

	// Output:
	// Range1: 10.1.1.1-10.1.1.2, Range2: 10.1.1.3-10.1.1.4, overlapped: false
	// Range1: 10.1.1.1-10.1.2.1, Range2: 10.1.1.254-10.1.1.255, overlapped: true
	// Range1: 10.1.1.1-10.1.1.6, Range2: 10.1.1.5-10.1.1.9, overlapped: true
	// Range1: 10.1.1.5-10.1.1.9, Range2: 10.1.1.1-10.1.1.6, overlapped: true
	// Range1: ::1-::2, Range2: ::3-::4, overlapped: false
	// Range1: ::1-::6, Range2: ::5-::9, overlapped: true
	// Range1: ::5-::9, Range2: ::1-::6, overlapped: true
	// Range1: 10.1.1.4, Range2: 10.1.1.1-10.1.1.6, overlapped: true
	// Range1: 10.1.1.1-10.1.1.6, Range2: 10.1.1.4, overlapped: true
	// Range1: 10.1.1.4, Range2: 10.1.1.4, overlapped: true
	// Range1: 10.1.1.4, Range2: 10.1.1.8-10.1.1.9, overlapped: false
	// Range1: 10.1.1.8-10.1.1.9, Range2: 10.1.1.4, overlapped: false
}

func TestGetIPScope(t *testing.T) {
	tests := []struct {
		ip       string
		expected string
	}{
		{
			ip:       "169.254.183.5",
			expected: "link",
		},
		{
			ip:       "fe80::1",
			expected: "link",
		},
		{
			ip:       "192.0.2.1",
			expected: "global",
		},
		{
			ip:       "::1",
			expected: "local",
		},
		{
			ip:       "127.0.0.1",
			expected: "local",
		},
		{
			ip:       "127::db8::1",
			expected: "global",
		},
		{
			ip:       "2001:db8::1",
			expected: "global",
		},
	}

	for _, test := range tests {
		t.Run(test.ip, func(t *testing.T) {
			scope := GetIPScope(test.ip)
			assert.Equal(t, test.expected, scope, "Expected scope to match")
		})
	}
}

func TestParseNetworks(t *testing.T) {
	testCases := []struct {
		name       string
		networks   string
		expectNets []string
		expectErr  bool
	}{
		{
			name:       "Single network",
			networks:   "10.0.0.0/24",
			expectNets: []string{"10.0.0.0/24"},
			expectErr:  false,
		},
		{
			name:       "Multiple networks",
			networks:   "10.0.0.0/24,192.168.0.1/32,127.0.0.0/8",
			expectNets: []string{"10.0.0.0/24", "192.168.0.1/32", "127.0.0.0/8"},
			expectErr:  false,
		},
		{
			name:       "Multiple networks with whitespace (tabs)",
			networks:   "10.0.0.0/24,  192.168.0.1/32,   127.0.0.0/8",
			expectNets: []string{"10.0.0.0/24", "192.168.0.1/32", "127.0.0.0/8"},
			expectErr:  false,
		},
		{
			name:       "Multiple networks with whitespace (newlines)",
			networks:   "10.0.0.0/24,\n192.168.0.1/32\n,\n127.0.0.0/8",
			expectNets: []string{"10.0.0.0/24", "192.168.0.1/32", "127.0.0.0/8"},
			expectErr:  false,
		},
		{
			name:       "Invalid network",
			networks:   "abcd.abcd/8",
			expectNets: nil,
			expectErr:  true,
		},
		{
			name:       "Multiple invalid networks",
			networks:   "600.600.600.600/24,abcd.abcd/8,abcd.abcd/8",
			expectNets: nil,
			expectErr:  true,
		},
		{
			name:       "Single invalid network in a list",
			networks:   "10.0.0.0/24,192.168.0.1/32,600.600.600.600/24,127.0.0.0/8",
			expectNets: nil,
			expectErr:  true,
		},
		{
			name:       "Single IPv6 network",
			networks:   "2001:db8:abcd:1::/64",
			expectNets: []string{"2001:db8:abcd:1::/64"},
			expectErr:  false,
		},
		{
			name:       "Multiple IPv6 networks",
			networks:   "2001:db8:abcd:1::/64,2001:db8:1234:1a00::/56",
			expectNets: []string{"2001:db8:abcd:1::/64", "2001:db8:1234:1a00::/56"},
			expectErr:  false,
		},
		{
			name:       "Mixed IPv4 and IPv6 networks",
			networks:   "2001:db8:abcd:1::/64,10.0.0.0/24,2001:db8:1234:1a00::/56",
			expectNets: []string{"2001:db8:abcd:1::/64", "10.0.0.0/24", "2001:db8:1234:1a00::/56"},
			expectErr:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nets, err := ParseNetworks(tc.networks)

			if tc.expectErr {
				assert.Error(t, err, "Expected ParseNetworks to return an error.")
			} else {
				assert.NoError(t, err, "Expected ParseNetworks to succeed.")
			}

			require.Len(t, tc.expectNets, len(nets), "Expected number of networks in/out to match.")

			for i, net := range nets {
				assert.Equal(t, net.String(), tc.expectNets[i], "Expected networks to match.")
			}
		})
	}
}
