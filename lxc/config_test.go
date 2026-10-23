package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigUnsetArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "Server key",
			args:     []string{"core.https_address"},
			expected: []string{"core.https_address="},
		},
		{
			name:     "Multiple server keys",
			args:     []string{"user.foo", "user.bar"},
			expected: []string{"user.foo=", "user.bar="},
		},
		{
			name:     "Server keys on a remote",
			args:     []string{"foo:", "user.foo", "user.bar"},
			expected: []string{"foo:", "user.foo=", "user.bar="},
		},
		{
			name:     "Instance key",
			args:     []string{"c1", "user.foo"},
			expected: []string{"c1", "user.foo="},
		},
		{
			name:     "Multiple instance keys",
			args:     []string{"c1", "user.foo", "limits.cpu"},
			expected: []string{"c1", "user.foo=", "limits.cpu="},
		},
		{
			name:     "Multiple instance keys on a remote",
			args:     []string{"foo:c1", "user.foo", "limits.cpu"},
			expected: []string{"foo:c1", "user.foo=", "limits.cpu="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setArgs, err := configUnsetArgs(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, setArgs)
		})
	}

	_, err := configUnsetArgs([]string{"c1", "user.foo=bar"})
	assert.Error(t, err)
}
