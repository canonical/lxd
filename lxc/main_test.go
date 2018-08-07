package main

import (
	"testing"

	"github.com/lxc/lxd/lxc/config"
)

type aliasTestcase struct {
	input    []string
	expected []string
}

func slicesEqual(a, b []string) bool {
	if a == nil && b == nil {
		return true
	}

	if a == nil || b == nil {
		return false
	}

	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestExpandAliases(t *testing.T) {
	aliases := map[string]string{
		"tester 12": "list",
		"foo":       "list @ARGS@ -c n",
		"ssh":       "/usr/bin/ssh @ARGS@",
	}

	testcases := []aliasTestcase{
		{
			input:    []string{"lxc", "list"},
			expected: []string{"lxc", "list"},
		},
		{
			input:    []string{"lxc", "tester", "12"},
			expected: []string{"lxc", "list"},
		},
		{
			input:    []string{"lxc", "foo", "asdf"},
			expected: []string{"lxc", "list", "asdf", "-c", "n"},
		},
		{
			input:    []string{"lxc", "ssh", "c1"},
			expected: []string{"/usr/bin/ssh", "c1"},
		},
	}

	conf := &config.Config{Aliases: aliases}

	for _, tc := range testcases {
		result, expanded := expandAlias(conf, tc.input)
		if !expanded {
			if !slicesEqual(tc.input, tc.expected) {
				t.Errorf("didn't expand when expected to: %s", tc.input)
			}
			continue
		}

		if !slicesEqual(result, tc.expected) {
			t.Errorf("%s didn't match %s", result, tc.expected)
		}
	}
}
