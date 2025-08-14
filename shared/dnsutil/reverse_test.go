package dnsutil

import (
	"net"
	"testing"
)

func Test_Reverse(t *testing.T) {
	tests := []struct {
		ip       net.IP
		expected string
	}{
		{
			ip:       net.ParseIP("192.0.2.1"),
			expected: "1.2.0.192.in-addr.arpa.",
		},
		{
			ip:       net.ParseIP("127.0.0.1"),
			expected: "1.0.0.127.in-addr.arpa.",
		},
		{
			ip:       net.ParseIP("2001:db8::1"),
			expected: "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.",
		},
		{
			ip:       net.ParseIP("::1"),
			expected: "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa.",
		},
		{
			ip:       nil,
			expected: "",
		},
	}

	for _, test := range tests {
		result := Reverse(test.ip)
		if result != test.expected {
			t.Errorf("Reverse(%q) = %q; want %q", test.ip, result, test.expected)
		}
	}
}
