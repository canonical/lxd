// Various utilities to generate/parse/manipulate SQL expressions.

package query

import (
	"fmt"
	"strings"
)

// Return a parameters expression with the given number of '?'
// placeholders. E.g. exprParams(2) -> "(?, ?)". Useful for
// IN expressions.
func exprParams(n int) string {
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		tokens[i] = "?"
	}
	return fmt.Sprintf("(%s)", strings.Join(tokens, ", "))
}
