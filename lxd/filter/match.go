package filter

import (
	"reflect"
	"regexp"
	"strings"
)

// Match returns true if the given object matches the given filter.
func Match(obj any, clauses []Clause) bool {
	match := true

	for _, clause := range clauses {
		value := ValueOf(obj, clause.Field)
		var clauseMatch bool

		// If 'value' is type of string try to test value as a regexp
		// Comparison is case insensitive
		if reflect.ValueOf(value).Kind() == reflect.String {
			regexpValue := clause.Value
			if !(strings.Contains(regexpValue, "^") || strings.Contains(regexpValue, "$")) {
				regexpValue = "^" + regexpValue + "$"
			}

			r, err := regexp.Compile("(?i)" + regexpValue)
			// If not regexp compatible use original value.
			if err != nil {
				clauseMatch = strings.EqualFold(value.(string), clause.Value)
			} else {
				clauseMatch = r.MatchString(value.(string))
			}
		} else {
			clauseMatch = value == clause.Value
		}

		if clause.Operator == "ne" {
			clauseMatch = !clauseMatch
		}

		// Finish out logic
		if clause.Not {
			clauseMatch = !clauseMatch
		}

		switch clause.PrevLogical {
		case "and":
			match = match && clauseMatch
		case "or":
			match = match || clauseMatch
		default:
			panic("unexpected clause operator")
		}
	}

	return match
}
