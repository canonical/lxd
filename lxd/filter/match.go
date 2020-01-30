package filter

// Match returns true if the given object matches the given filter.
func Match(obj interface{}, clauses []Clause) bool {
	match := true

	for _, clause := range clauses {
		value := ValueOf(obj, clause.Field)
		clauseMatch := value == clause.Value

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
