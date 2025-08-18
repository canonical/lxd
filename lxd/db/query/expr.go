// Various utilities to generate/parse/manipulate SQL expressions.

package query

import (
	"strconv"
	"strings"
)

// Params returns a parameters expression with the given number of '?'
// placeholders. E.g. Params(2) -> "(?, ?)". Useful for IN and VALUES
// expressions.
func Params(n int) string {
	tokens := make([]string, n)
	for i := range n {
		tokens[i] = "?"
	}

	return "(" + strings.Join(tokens, ", ") + ")"
}

// IntParams returns a parameters expression with the given integer(s).
// E.g. IntParams(1, 2, 3) -> "(1, 2, 3)". Useful for IN expressions and VALUES expressions.
func IntParams[T int | int8 | int16 | int32 | int64 | uint | uint8 | uint16 | uint32 | uint64](args ...T) string {
	strs := make([]string, 0, len(args))
	for _, a := range args {
		if a > 0 {
			strs = append(strs, strconv.FormatUint(uint64(a), 10))
			continue
		}

		strs = append(strs, strconv.FormatInt(int64(a), 10))
	}

	return "(" + strings.Join(strs, ", ") + ")"
}
