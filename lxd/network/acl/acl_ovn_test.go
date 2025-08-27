package acl

import (
	"testing"
)

func Test_ovnRulePortToOVNACLMatch(t *testing.T) {
	tests := []struct {
		name         string
		protocol     string
		direction    string
		portCriteria []string
		expected     string
	}{
		{
			name:         "Multiple individual ports",
			protocol:     "tcp",
			direction:    "dst",
			portCriteria: []string{"8080", "9090"},
			expected:     "tcp.dst == 8080 || tcp.dst == 9090",
		},
		{
			name:         "Single port range",
			protocol:     "tcp",
			direction:    "dst",
			portCriteria: []string{"8000-8080"},
			expected:     "(tcp.dst >= 8000 && tcp.dst <= 8080)",
		},
		{
			name:         "Mixed ports and ranges",
			protocol:     "udp",
			direction:    "src",
			portCriteria: []string{"8080", "9090", "8000-8080"},
			expected:     "udp.src == 8080 || udp.src == 9090 || (udp.src >= 8000 && udp.src <= 8080)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ovnRulePortToOVNACLMatch(tt.protocol, tt.direction, tt.portCriteria...)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func Benchmark_ovnRulePortToOVNACLMatch(b *testing.B) {
	for b.Loop() {
		ovnRulePortToOVNACLMatch("tcp", "dst", "8080", "9090", "8000-8080")
	}
}
