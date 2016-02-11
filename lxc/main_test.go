package main

import (
	"testing"

	"github.com/lxc/lxd"
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
	}

	testcases := []aliasTestcase{
		aliasTestcase{
			input:    []string{"lxc", "list"},
			expected: []string{"lxc", "list"},
		},
		aliasTestcase{
			input:    []string{"lxc", "tester", "12"},
			expected: []string{"lxc", "list", "--no-alias"},
		},
		aliasTestcase{
			input:    []string{"lxc", "foo", "asdf"},
			expected: []string{"lxc", "list", "--no-alias", "asdf", "-c", "n"},
		},
	}

	conf := &lxd.Config{Aliases: aliases}

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
