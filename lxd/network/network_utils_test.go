package network

import (
	"fmt"
	"net"
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
		"0.0.0.1-01.0.0.255",
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
		parsedRange, err := parseIPRange(ipRange, allowedv4NetworkA, allowedv4NetworkB, allowedv6NetworkA, allowedv6NetworkB)
		if err != nil {
			fmt.Printf("Err: %v\n", err)
			continue
		}

		fmt.Printf("Start: %s, End: %s\n", parsedRange.Start.String(), parsedRange.End.String())
	}

	fmt.Println("Without allowed networks")
	for _, ipRange := range ipRanges {
		parsedRange, err := parseIPRange(ipRange)
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
	// Err: IP range "0.0.0.1-01.0.0.255" does not fall within any of the allowed networks [192.168.1.0/24 192.168.0.0/16 fd22:c952:653e:3df6::/64 fd22:c952:653e::/48]
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
