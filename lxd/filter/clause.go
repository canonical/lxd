package filter

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared"
)

// Clause is a single filter clause in a filter string.
type Clause struct {
	PrevLogical string
	Not         bool
	Field       string
	Operator    string
	Value       string
}

// Parse a user-provided filter string.
func Parse(s string) ([]Clause, error) {
	clauses := []Clause{}

	parts := strings.Fields(s)

	index := 0
	prevLogical := "and"

	for index < len(parts) {
		clause := Clause{}

		if strings.EqualFold(parts[index], "not") {
			clause.Not = true
			index++
			if index == len(parts) {
				return nil, fmt.Errorf("incomplete not clause")
			}
		} else {
			clause.Not = false
		}

		clause.Field = parts[index]

		index++
		if index == len(parts) {
			return nil, fmt.Errorf("clause has no operator")
		}
		clause.Operator = parts[index]

		index++
		if index == len(parts) {
			return nil, fmt.Errorf("clause has no value")
		}
		value := parts[index]

		// support strings with spaces that are quoted
		if strings.HasPrefix(value, "\"") {
			value = value[1:]
			for {
				index++
				if index == len(parts) {
					return nil, fmt.Errorf("unterminated quote")
				}
				if strings.HasSuffix(parts[index], "\"") {
					break
				}
				value += " " + parts[index]
			}
			end := parts[index]
			value += " " + end[0:len(end)-1]
		}
		clause.Value = value
		index++

		clause.PrevLogical = prevLogical
		if index < len(parts) {
			prevLogical = parts[index]
			if !shared.StringInSlice(prevLogical, []string{"and", "or"}) {
				return nil, fmt.Errorf("invalid clause composition")
			}
			index++
			if index == len(parts) {
				return nil, fmt.Errorf("unterminated compound clause")
			}
		}
		clauses = append(clauses, clause)
	}

	return clauses, nil
}
