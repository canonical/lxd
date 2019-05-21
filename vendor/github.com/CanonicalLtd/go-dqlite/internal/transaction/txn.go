package transaction

import (
	"fmt"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/ryanfaerman/fsm"
)

// Txn captures information about an active WAL write transaction that has been
// started on a SQLite connection configured to be in either leader or follower
// replication mode.
type Txn struct {
	conn     *bindings.Conn // Underlying SQLite db.
	id       uint64         // Transaction ID.
	machine  fsm.Machine    // Internal fsm for validating state changes.
	isLeader bool           // Whether our connection is in leader mode.
	isZombie bool           // Whether this is a zombie transaction, see Zombie().
	dryRun   bool           // Dry run mode, don't invoke actual SQLite hooks.

	// For leader transactions, these are the parameters of all non-commit
	// frames commands that were executed so far during this
	// transaction.
	//
	// They are used in case the last commit frames command failed with
	// ErrLeadershipLost, and either the same server gets re-elected or a
	// quorum was reached despite the glitch and another server was
	// elected. In that situation the server that lost leadership in the
	// first place will need to replay the whole transaction using a
	// follower connection, since its transaction (associated with a leader
	// connection) was rolled back by SQLite.
	frames []bindings.WalReplicationFrameInfo
}

// New creates a new Txn instance.
func New(conn *bindings.Conn, id uint64) *Txn {
	return &Txn{
		conn:    conn,
		id:      id,
		machine: newMachine(),
	}
}

func (t *Txn) String() string {
	s := fmt.Sprintf("%d %s as ", t.id, t.State())
	if t.IsLeader() {
		s += "leader"
		if t.IsZombie() {
			s += " (zombie)"
		}
	} else {
		s += "follower"
	}
	return s
}

// Leader marks this transaction as a leader transaction.
//
// A leader transaction is automatically set to dry-run, since the SQLite will
// trigger itself the relevant WAL APIs when transitioning between states.
//
// Depending on the particular replication hook being executed SQLite might do
// that before or after the hook. See src/pager.c in SQLite source code for
// details about when WAL APis are invoked exactly with respect to the various
// sqlite3_replication_methods hooks.
func (t *Txn) Leader() {
	if t.isLeader {
		panic("transaction is already marked as leader")
	}
	t.isLeader = true
	t.DryRun()
}

// IsLeader returns true if the underlying connection is in leader
// replication mode.
func (t *Txn) IsLeader() bool {
	return t.isLeader
}

// DryRun makes this transaction only transition between states, without
// actually invoking the relevant SQLite APIs.
//
// This is used to create a surrogate follower, and for tests.
func (t *Txn) DryRun() {
	if t.dryRun {
		panic("transaction is already in dry-run mode")
	}
	t.dryRun = true
}

// Conn returns the sqlite connection that started this write
// transaction.
func (t *Txn) Conn() *bindings.Conn {
	return t.conn
}

// ID returns the ID associated with this transaction.
func (t *Txn) ID() uint64 {
	return t.id
}

// State returns the current state of the transaction.
func (t *Txn) State() fsm.State {
	return t.machine.Subject.CurrentState()
}

// Frames writes frames to the WAL.
func (t *Txn) Frames(begin bool, info bindings.WalReplicationFrameInfo) error {
	state := Writing
	if info.IsCommitGet() {
		state = Written
	}
	return t.transition(state, begin, info)
}

// Undo reverts all changes to the WAL since the start of the
// transaction.
func (t *Txn) Undo() error {
	return t.transition(Undone)
}

// Zombie marks this transaction as zombie. It must be called only for leader
// transactions.
//
// A zombie transaction is one whose leader has lost leadership while applying
// the associated FSM command. The transaction is left in state passed as
// argument.
func (t *Txn) Zombie() {
	if !t.isLeader {
		panic("follower transactions can't be marked as zombie")
	}
	if t.isZombie {
		panic("transaction is already marked as zombie")
	}
	t.isZombie = true
}

// IsZombie returns true if this is a zombie transaction.
func (t *Txn) IsZombie() bool {
	if !t.isLeader {
		panic("follower transactions can't be zombie")
	}
	return t.isZombie
}

// Resurrect a zombie transaction.
//
// This should be called only on zombie transactions in Pending or Writing
// state, in case a leader that lost leadership was re-elected right away or a
// quorum for a lost commit frames command was reached and the new leader is
// replicating it on the former leader.
//
// A new follower transaction will be created with the given connection (which
// is assumed to be in follower replication mode), and set to the same ID as
// this zombie.
//
// All preceeding non-commit frames commands (if any) will be re-applied on the
// follower transaction.
//
// If no error occurrs, the newly created follower transaction is returned.
func (t *Txn) Resurrect(conn *bindings.Conn) (*Txn, error) {
	if !t.isLeader {
		panic("attempt to resurrect follower transaction")
	}
	if !t.isZombie {
		panic("attempt to resurrect non-zombie transaction")
	}
	if t.State() != Pending && t.State() != Writing {
		panic("attempt to resurrect a transaction not in pending or writing state")
	}
	txn := New(conn, t.ID())

	for i, frames := range t.frames {
		begin := i == 0
		if err := txn.transition(Writing, begin, frames); err != nil {
			return nil, err
		}
	}

	return txn, nil
}

// Try to transition to the given state. If the transition is invalid,
// panic out.
func (t *Txn) transition(state fsm.State, args ...interface{}) error {
	if err := t.machine.Transition(state); err != nil {
		panic(fmt.Sprintf("invalid %s -> %s transition", t.State(), state))
	}

	if t.isLeader {
		// In leader mode, don't actually invoke SQLite replication
		// API, since that will be done by SQLite internally.
		switch state {
		case Writing:
			// Save non-commit frames in case the last commit fails
			// and gets recovered by the same leader.
			begin := args[0].(bool)
			frames := args[1].(bindings.WalReplicationFrameInfo)
			if begin {
				t.frames = append(t.frames, frames)
			}
		case Written:
			fallthrough
		case Undone:
			// Reset saved frames. They are not going to be used
			// anymore and they help garbage-collecting them, since
			// the tracer holds references to a number of
			// transaction objects.
			t.frames = nil
		}
	}

	if t.dryRun {
		// In dry run mode, don't actually invoke any SQLite API.
		return nil
	}

	var err error
	switch state {
	case Writing:
		fallthrough
	case Written:
		//begin := args[0].(bool)
		info := args[1].(bindings.WalReplicationFrameInfo)
		err = t.conn.WalReplicationFrames(info)
	case Undone:
		err = t.conn.WalReplicationUndo()
	}

	if err != nil {
		if err := t.machine.Transition(Doomed); err != nil {
			panic(fmt.Sprintf("cannot doom from %s", t.State()))
		}
	}

	return err
}
