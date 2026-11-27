package acl

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/canonical/lxd/shared"
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

func Test_journalctlNoMatches(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		expected bool
	}{
		{
			name:     "Exit status 1 without output",
			script:   "exit 1",
			expected: true,
		},
		{
			name:     "Exit status 1 with an error on stderr",
			script:   "echo 'Bad pattern' >&2; exit 1",
			expected: false,
		},
		{
			name:     "Exit status 1 with output on stdout",
			script:   "echo '{}'; exit 1",
			expected: false,
		},
		{
			name:     "Other exit status",
			script:   "exit 2",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := bytes.Buffer{}
			err := shared.RunCommandWithFds(context.Background(), nil, &stdout, "sh", "-c", tt.script)
			if err == nil {
				t.Fatal("Expected the command to fail")
			}

			got := journalctlNoMatches(err, &stdout)
			if got != tt.expected {
				t.Errorf("journalctlNoMatches() = %v, expected %v", got, tt.expected)
			}
		})
	}

	if journalctlNoMatches(errors.New("Failed starting journalctl"), &bytes.Buffer{}) {
		t.Error("journalctlNoMatches() should be false for errors without an exit status")
	}
}
