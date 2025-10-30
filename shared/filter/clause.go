package filter

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Clause is a single filter clause in a filter string.
type Clause struct {
	PrevLogical string
	Not         bool
	Field       string
	Operator    string
	Value       string
}

// ClauseSet is a set of clauses. There are configurable functions that can be used to
// perform unique parsing of the clauses.
type ClauseSet struct {
	Clauses []Clause
	Ops     OperatorSet

	ParseInt         func(Clause) (int64, error)
	ParseUint        func(Clause) (uint64, error)
	ParseString      func(Clause) (string, error)
	ParseBool        func(Clause) (bool, error)
	ParseRegexp      func(Clause) (*regexp.Regexp, error)
	ParseStringSlice func(Clause) ([]string, error)
}

// Parse a user-provided filter string.
func Parse(s string, op OperatorSet) (*ClauseSet, error) {
	if !op.isValid() {
		return nil, errors.New("Invalid operator set")
	}

	clauses := []Clause{}

	parts := strings.Fields(s)

	index := 0
	prevLogical := op.And

	for index < len(parts) {
		clause := Clause{}

		if strings.EqualFold(parts[index], op.Negate) {
			clause.Not = true
			index++
			if index == len(parts) {
				return nil, errors.New("incomplete not clause")
			}
		} else {
			clause.Not = false
		}

		clause.Field = parts[index]

		index++
		if index == len(parts) {
			return nil, errors.New("clause has no operator")
		}

		clause.Operator = parts[index]

		index++
		if index == len(parts) {
			return nil, errors.New("clause has no value")
		}

		var value strings.Builder
		value.WriteString(parts[index])

		// support strings with spaces that are quoted
		for _, symbol := range op.Quote {
			if strings.HasPrefix(parts[index], symbol) {
				value.Reset()
				value.WriteString(parts[index][1:])
				for {
					index++
					if index == len(parts) {
						return nil, errors.New("unterminated quote")
					}

					if strings.HasSuffix(parts[index], symbol) {
						break
					}

					value.WriteString(" " + parts[index])
				}

				end := parts[index]
				value.WriteString(" " + end[0:len(end)-1])
			}
		}

		clause.Value = value.String()
		index++

		clause.PrevLogical = prevLogical
		if index < len(parts) {
			prevLogical = parts[index]
			if !slices.Contains([]string{op.And, op.Or}, prevLogical) {
				return nil, errors.New("invalid clause composition")
			}

			index++
			if index == len(parts) {
				return nil, errors.New("unterminated compound clause")
			}
		}

		clauses = append(clauses, clause)
	}

	return &ClauseSet{Clauses: clauses, Ops: op}, nil
}
