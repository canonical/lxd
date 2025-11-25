package lex

import (
	"fmt"
	"strings"
)

// KeyValue extracts the key and value encoded in the given string and
// separated by '=' (foo=bar -> foo, bar).
func KeyValue(s string) (key string, value string, err error) {
	key, value, found := strings.Cut(s, "=")

	if !found {
		return "", "", fmt.Errorf("The token %q is not a key/value pair", s)
	}

	return key, value, nil
}
