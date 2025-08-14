package dnsutil

import (
	"testing"
)

func Test_ExtractAddressFromReverse(t *testing.T) {
	tests := []struct {
		reverseName string
		expected    string
	}{
		{
			reverseName: "1.2.0.192.in-addr.arpa.",
			expected:    "192.0.2.1",
		},
		{
			reverseName: "1.2.0.192",
			expected:    "",
		},
		{
			reverseName: "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.",
			expected:    "2001:db8::1",
		},
		{
			reverseName: "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2",
			expected:    "",
		},
		{
			reverseName: "b.o.g.u.s.in-addr.arpa.",
			expected:    "",
		},
		{
			reverseName: "b.o.g.u.s.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.",
			expected:    "",
		},
		{
			reverseName: "invalid.reverse.name",
			expected:    "",
		},
	}

	for _, test := range tests {
		result := ExtractAddressFromReverse(test.reverseName)
		if result != test.expected {
			t.Errorf("ExtractAddressFromReverse(%q) = %q; want %q", test.reverseName, result, test.expected)
		}
	}
}

func Test_IsReverse(t *testing.T) {
	tests := []struct {
		name     string
		expected int
	}{
		{
			name:     "example.com",
			expected: 0,
		},
		{
			name:     "1.2.0.192.in-addr.arpa.",
			expected: 1,
		},
		{
			name:     "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.",
			expected: 2,
		},
	}

	for _, test := range tests {
		result := IsReverse(test.name)
		if result != test.expected {
			t.Errorf("IsReverse(%q) = %d; want %d", test.name, result, test.expected)
		}
	}
}
