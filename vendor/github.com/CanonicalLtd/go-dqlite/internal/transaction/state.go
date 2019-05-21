package transaction

import (
	"github.com/ryanfaerman/fsm"
)

// Possible transaction states. Most states are associated with SQLite
// replication hooks that are invoked upon transitioning from one lifecycle
// state to the next.
const (
	Pending = fsm.State("pending") // Initial state right after creation.
	Writing = fsm.State("writing") // After a non-commit frames command has been executed.
	Written = fsm.State("written") // After a final commit frames command has been executed.
	Undone  = fsm.State("undone")  // After an undo command has been executed.
	Doomed  = fsm.State("doomed")  // The transaction has errored.
)

// Create a new FSM initialized with a fresh state object set to Pending.
func newMachine() fsm.Machine {
	return fsm.New(
		fsm.WithRules(newRules()),
		fsm.WithSubject(newState()),
	)
}

// Capture valid state transitions within a transaction.
func newRules() fsm.Ruleset {
	rules := fsm.Ruleset{}

	for o, states := range transitions {
		for _, e := range states {
			rules.AddTransition(fsm.T{O: o, E: e})
		}
	}

	return rules
}

// Map of all valid state transitions.
var transitions = map[fsm.State][]fsm.State{
	Pending: {Writing, Written, Undone},
	Writing: {Writing, Written, Undone, Doomed},
	Written: {Doomed},
	Undone:  {Doomed},
}

// Track the state of transaction. Implements the fsm.Stater interface.
type state struct {
	state fsm.State
}

// Return a new transaction state object, set to Pending.
func newState() *state {
	return &state{
		state: Pending,
	}
}

// CurrentState returns the current state, implementing fsm.Stater.
func (s *state) CurrentState() fsm.State {
	return s.state
}

// SetState switches the current state, implementing fsm.Stater.
func (s *state) SetState(state fsm.State) {
	s.state = state
}
