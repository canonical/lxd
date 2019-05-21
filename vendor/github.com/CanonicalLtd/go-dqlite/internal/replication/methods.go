// Copyright 2017 Canonical Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package replication

import (
	"sync"
	"time"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/CanonicalLtd/go-dqlite/internal/protocol"
	"github.com/CanonicalLtd/go-dqlite/internal/registry"
	"github.com/CanonicalLtd/go-dqlite/internal/trace"
	"github.com/CanonicalLtd/go-dqlite/internal/transaction"
	"github.com/hashicorp/raft"
)

// Methods implements the SQLite replication C API using the sqlite3 bindings.
type Methods struct {
	registry     *registry.Registry
	raft         *raft.Raft   // Raft engine to use
	mu           sync.RWMutex // TODO: make this lock per-database.
	applyTimeout time.Duration

	// If greater than zero, skip the initial not-leader checks, this
	// amount of times. Only used for testing.
	noLeaderCheck int
}

// NewMethods returns a new Methods instance that can be used as callbacks API
// for raft-based SQLite replication of a single connection.
func NewMethods(reg *registry.Registry, raft *raft.Raft) *Methods {
	return &Methods{
		registry:     reg,
		raft:         raft,
		applyTimeout: 10 * time.Second,
	}
}

// ApplyTimeout sets the maximum amount of time to wait before giving
// up applying a raft command. The default is 10 seconds.
func (m *Methods) ApplyTimeout(timeout time.Duration) {
	m.applyTimeout = timeout
}

// Begin is the hook invoked by SQLite when a new write transaction is
// being started within a connection in leader replication mode on
// this server.
func (m *Methods) Begin(conn *bindings.Conn) int {
	// We take a the lock for the entire duration of the hook to avoid
	// races between to concurrent hooks.
	//
	// The main tasks of Begin are to check that no other write transaction
	// is in progress and to cleanup any dangling follower transactions
	// that might have been left open after a leadership change.
	//
	// Concurrent calls can happen because the xBegin hook is fired by
	// SQLite before acquiring a write lock on the WAL (i.e. before calling
	// WalBeginWriteTransaction), so different connections can enter the
	// Begin hook at any time .
	//
	// Some races that is worth mentioning are:
	//
	//  - Two concurrent calls of Begin: without the lock, they would race
	//    to open a follower connection if it does not exists yet.
	//
	//  - Two concurrent calls of Begin and another hook: without the lock,
	//    they would race to mutate the registry (e.g. add/delete
	//    transactions).
	//
	// The most common errors that Begin returns are:
	//
	//  - SQLITE_BUSY:  If we detect that a write transaction is in progress
	//                  on another connection. The SQLite's call to sqlite3PagerBegin
	//                  that triggered the xBegin hook will propagate the error
	//                  to sqlite3BtreeBeginTrans, which will invoke the busy
	//                  handler (if set), calling xBegin/Begin again, which will
	//                  keep returning SQLITE_BUSY as long as the other transaction
	//                  is in progress. If the busy handler gives up, the SQLITE_BUSY
	//                  error will bubble up, and the statement that triggered the
	//                  write attempt will fail. The client should then execute a
	//                  ROLLBACK and then decide what to do.
	//
	//  - SQLITE_IOERR: This is returned if we are not the leader when the
	//                  hook fires or if we fail to apply the Open follower
	//                  command log (in case no follower for this database
	//                  is open yet). We include the relevant extended
	//                  code, either SQLITE_IOERR_NOT_LEADER if this server
	//                  is not the leader anymore or it is being shutdown,
	//                  or SQLITE_IOERR_LEADERSHIP_LOST if leadership was
	//                  lost while applying the Open command. The SQLite's
	//                  call to sqlite3PagerBegin that triggered the xBegin
	//                  hook will propagate the error to sqlite3BtreeBeginTrans,
	//                  which will in turn propagate it to the OP_Transaction case
	//                  of vdbe.c, which will goto abort_due_to_error and finally
	//                  call sqlite3VdbeHalt, automatically rolling back the
	//                  transaction. Since no write transaction was actually started the
	//                  xEnd hook is not fired.
	//
	// We might return SQLITE_INTERRUPT or SQLITE_INTERNAL in case of more exotic
	// failures. See the apply() method for details.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Lock the registry, to avoid races with the FSM when checking for the
	// presence of a hook sync and when modifying transactions.
	m.registry.Lock()
	defer m.registry.Unlock()

	// Enable synchronization with the FSM: it will only execute commands
	// applied during this hook, and block applying any other command until
	// this hook is done.
	m.registry.HookSyncSet()
	defer m.registry.HookSyncReset()

	tracer := m.registry.TracerConn(conn, "begin")
	tracer.Message("start")

	// Check if we're the leader.
	if m.noLeaderCheck == 0 && m.raft.State() != raft.Leader {
		// No dqlite state has been modified, and the WAL write lock
		// has not been acquired. Just return ErrIoErrNotLeader.
		tracer.Message("not leader")
		return bindings.ErrIoErrNotLeader
	}

	// Update the noLeaderCheck counter (used for tests).
	if m.noLeaderCheck > 0 {
		m.noLeaderCheck--
	}

	// Possibly open a follower for this database if it doesn't exist yet.
	if err := m.beginMaybeAddFollowerConn(tracer, conn); err != nil {
		// Since we haven't yet registered a transaction, there's no
		// cleanup to do here. The worst that can happen is that the
		// Raft.Apply() call failed with ErrLeadershipLost and a quorum
		// for the log will actually be reached. In that case all FSMs
		// (including our own) will apply the open command.
		return errno(err)
	}

	// Check whether there is already an an ongoing transaction.
	if err := m.beginMaybeHandleInProgressTxn(tracer, conn); err != nil {
		return errno(err)
	}

	// Use the last applied index as transaction ID.
	//
	// If this server is still the leader, this number is guaranteed to be
	// strictly higher than any previous transaction ID, since after a
	// leadership change we always call raft.Barrier() to advance the FSM
	// up to the latest committed log, raft.Barrier() itself will increment
	// the applied index by one.
	//
	// If it's not the leader anymore, it does not matter which ID we pick
	// because any coming Frames or Undo hook will fail with ErrNotLeader.
	txid := m.raft.AppliedIndex()

	tracer = tracer.With(trace.Integer("txn", int64(txid)))
	tracer.Message("register transaction")

	// Create a new transaction.
	m.registry.TxnLeaderAdd(conn, txid)

	tracer.Message("done")

	return 0
}

// Check if a follower connection is already open for this database, if not
// open one with the Open raft command.
func (m *Methods) beginMaybeAddFollowerConn(tracer *trace.Tracer, conn *bindings.Conn) error {
	filename := m.registry.ConnLeaderFilename(conn)

	if m.registry.ConnFollowerExists(filename) {
		return nil
	}

	tracer.Message("open follower for %s", filename)
	return m.apply(tracer, conn, protocol.NewOpen(filename))
}

// This method ensures that there is no other write transactions happening
// on this node against database associated to the given connection.
//
// If one is found, this method will try take appropriate measures.
//
// If an error is returned, Begin should stop and return it.
func (m *Methods) beginMaybeHandleInProgressTxn(tracer *trace.Tracer, conn *bindings.Conn) error {
	filename := m.registry.ConnLeaderFilename(conn)
	txn := m.registry.TxnByFilename(filename)
	if txn == nil {
		return nil
	}

	tracer.Message("found in-progress transaction %s", txn)

	// Check if the in-progress transaction is a concurrent leader.
	if txn.IsLeader() {
		if txn.Conn() != conn {
			// This means that there is a transaction in progress
			// originated on this Methods instance for another
			// connection.
			//
			// We can't proceed as the Begin method would then try
			// to add a new transaction to the registry and crash.
			//
			// No dqlite state has been modified, and the WAL write
			// lock has not been acquired.
			//
			// We just return ErrBusy, which has the same effect as
			// the call to sqlite3WalBeginWriteTransaction (invoked
			// in pager.c after a successful xBegin) would have, i.e.
			// the busy handler will end up polling us again until
			// the concurrent write transaction ends and we're free
			// to go.
			tracer.Message("busy")
			return bindings.Error{Code: bindings.ErrBusy}
		}

		// There a's transaction originated on this Methods instance for
		// the same connection.
		if !txn.IsZombie() {
			// This should be an impossible situation since it
			// would mean that the same connection managed to begin
			// a new transaction on the same connection, something
			// that SQLite prevents.
			tracer.Panic("unexpected transaction on same connection %s", txn)
		}

		// If we have a zombie for this connection it, must mean that a
		// Frames command failed because we were not leaders anymore at
		// that time and this was a commit frames command following one
		// or more non-commit frames commands that were successfully
		// applied.
		if txn.State() != transaction.Writing {
			tracer.Panic("unexpected transaction %s", txn)
		}

		// Create a surrogate follower and revert the transaction just
		// below.
		m.surrogateWriting(tracer, txn)
	}

	tracer.Message("undo stale transaction %s", txn)
	if err := m.apply(tracer, conn, protocol.NewUndo(txn.ID())); err != nil {
		// Whatever the reason of the failure is (not leader or
		// leadeship lost), we can leave things as they are,
		// since the next leader should try to run again the
		// undo command.
		return err
	}

	return nil
}

// Abort is the hook invoked by SQLite when a write transaction fails
// to begin.
func (m *Methods) Abort(conn *bindings.Conn) int {
	// We take a the lock for the entire duration of the hook to avoid
	// races between to cocurrent hooks.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Lock the registry.
	m.registry.Lock()
	defer m.registry.Unlock()

	tracer := m.registry.TracerConn(conn, "abort")
	tracer.Message("start")

	// This is only called if SQLite fails to start a WAL write transaction.
	txn := m.registry.TxnByConn(conn)
	if txn == nil {
		tracer.Panic("no in-progress transaction")
	}
	tracer.Message("found txn %s", txn)

	// Sanity checks.
	if !txn.IsLeader() || txn.Conn() != conn {
		tracer.Panic("unexpected transaction %s", txn)
	}
	if txn.State() != transaction.Pending {
		tracer.Panic("unexpected transaction state %s", txn)
	}

	tracer.Message("discard aborted transaction")
	m.registry.TxnDel(txn.ID())

	return 0
}

// Frames is the hook invoked by sqlite when new frames need to be
// flushed to the write-ahead log.
func (m *Methods) Frames(conn *bindings.Conn, frames bindings.WalReplicationFrameList) int {
	// We take a the lock for the entire duration of the hook to avoid
	// races between to cocurrent hooks. See the comments in Begin for more
	// details.
	//
	// The xFrames hook is invoked by the SQLite pager in two cases, either
	// in sqlite3PagerCommitPhaseOne (for committing) or in pagerStress (for
	// flushing dirty pages to disk, invoked by the pcache implementation when
	// no more space is available in the built-in page cache).
	//
	// In the first case, any error returned here will be propagated up to
	// sqlite3VdbeHalt (the final step of SQLite's VDBE), which will rollback
	// the transaction and indirectly invoke sqlite3PagerRollback which in turn
	// will indirectly fire xUndo and xEnd.
	//
	// In the second case, any error returned here will transition the
	// pager to the ERROR state (see the final pager_error call in
	// pagerStress) and will be propagated first to sqlite3PcacheFetchStress
	// and the indirectly to the btree layer which will automatically rollback
	// the transaction. The xUndo and xEnd hooks won't be fired, since the
	// pager is in an error state.
	//
	// The most common errors that Frames returns are:
	//
	m.mu.Lock()
	defer m.mu.Unlock()

	// Lock the registry.
	m.registry.Lock()
	defer m.registry.Unlock()

	// Enable synchronization with the FSM: it will only execute commands
	// applied during this hook, and block applying any other command until
	// this hook is done.
	m.registry.HookSyncSet()
	defer m.registry.HookSyncReset()

	tracer := m.registry.TracerConn(conn, "frames")
	tracer.Message("start (commit=%v)", frames.IsCommit())

	txn := m.registry.TxnByConn(conn)
	if txn == nil {
		tracer.Panic("no in-progress transaction")
	}
	tracer.Message("found txn %s", txn)

	// Sanity checks.
	if !txn.IsLeader() {
		tracer.Panic("unexpected transaction %s", txn)
	}
	if txn.State() != transaction.Pending && txn.State() != transaction.Writing {
		tracer.Panic("unexpected transaction state %s", txn)
	}

	// Check if we're the leader.
	if m.noLeaderCheck == 0 && m.raft.State() != raft.Leader {
		return errno(m.framesNotLeader(tracer, txn))
	}

	// Update the noLeaderCheck counter.
	if m.noLeaderCheck > 0 {
		m.noLeaderCheck--
	}

	filename := m.registry.ConnLeaderFilename(conn)
	command := protocol.NewFrames(txn.ID(), filename, frames)
	if err := m.apply(tracer, conn, command); err != nil {
		// Check that transaction is still Pending or Writing. The hook-sync
		// mechanism prevents our FSM to apply anything else, but let's
		// assert it for sanity.
		if txn.State() != transaction.Pending && txn.State() != transaction.Writing {
			tracer.Panic("unexpected transaction state: %s", txn)
		}

		if isErrNotLeader(err) {
			// This is relatively unlikely since we already checked
			// for leadership at the beginning of the hook, but
			// still possible in principle with a particular bad
			// timing.
			//
			// The same logic applies.
			//
			// We can be sure that the Frames command didn't get
			// committed, so we can just mark the transaction as
			// stale, create a surrogate follower and return. The
			// Undo hook that will be fired right after and will
			// no-op.
			return errno(m.framesNotLeader(tracer, txn))
		} else if isErrLeadershipLost(err) {
			if frames.IsCommit() {
				// Mark the transaction as zombie. Possible scenarios:
				//
				// 1. This server gets re-elected right away as leader.
				//
				//    In this case we'll try to apply this lost
				//    command log again. If we succeed, our FSM
				//    will transition this zombie transaction
				//    into to a surrogate follower and our next
				//    Begin hook invokation will issue an Undo
				//    command, which (if successfull) will be a
				//    no-op on our FSM and an actual rollback
				//    on the followers (regardless of whether
				//    this was the first non-commit frames
				//    command or a further one). If we fail to
				//    re-apply the command there will be a new
				//    election, and we'll end up again in
				//    either this case (1) or the next one
				//    (2). Same if the Undo command fails.
				//
				// 2. Another server gets elected as leader.
				//
				//    In this case there are two possible
				//    scenarios.
				//
				//    2.1. No quorum was reached for the lost
				//         commit command. This means that no
				//         FSM (including ours) will ever try
				//         to apply it. If this lost non-commit
				//         frames command was the first one of
				//         a transaction, the new leader will
				//         see no dangling follower and will
				//         just start a new transaction with a
				//         new ID, sending a Frames command to
				//         our FSM. Our FSM will detect the
				//         zombie transaction and simply purge
				//         it from the registry.
				//
				//    2.2 A quorum was reached for the lost
				//        commit command. This means that the
				//        new leader will replicate it to every
				//        server that didn't apply it yet,
				//        which includes us, and then issue an
				//        Undo command to abort the
				//        transaction. In this case our FSM
				//        will behave like in case 1.
				tracer.Message("marking as zombie")
				txn.Zombie()
			} else {
				// Mark the transaction as zombie. Possible scenarios:
				//
				// 1. This server gets re-elected right away as leader.
				//
				//    In this case we'll try to apply this lost
				//    command log again. If we succeed, two
				//    scenarios are possible:
				//
				//    1.1 This was the only Frames command in
				//        the transaction, our FSM will convert
				//        the zombie transaction into a
				//        follower transaction, and apply it
				//        normally, effectively recovering the
				//        commit failure.
				//
				//    2.1 This was the last Frames command in a
				//        series of one or more non-commit
				//        Frames commands, which were all
				//        applied.
				//
				// 2. Another server gets elected as leader.
				//
				//    In this case there are two possible
				//    scenarios.
				//
				//    2.1. No quorum was reached for the lost
				//         commit command. This means that no
				//         FSM (including ours) will ever try
				//         to apply it. If this lost commit
				//         frames command was the first one of
				//         a transaction, the new leader will
				//         see no dangling follower and will
				//         just start a new transaction with a
				//         new ID, sending a Frames command to
				//         our FSM. Our FSM will detect the
				//         zombie transaction and simply purge
				//         it from the registry.
				//
				//    2.2 A quorum was reached for the lost
				//        commit command. This means that the
				//        new leader will replicate it to every
				//        server that didn't apply it yet,
				//        which includes us. In this case our
				//        FSM will detect the zombie and
				//        resurrect it using the follower
				//        connection for this database, and
				//        possibly writing all preceeding
				//        non-commit frames to fully recover
				//        the transaction (which was originally
				//        rolled back on this server).
				tracer.Message("marking as zombie")
				txn.Zombie()
			}
		}

		// TODO: under which circumstances can we get errors other than
		// NotLeader/RaftShutdown and LeadershipLost? How to handle
		// them? See also the comments in the apply() method.

		return errno(err)
	}

	tracer.Message("done")

	return 0
}

// Handle Frames failures due to this server not not being the leader.
func (m *Methods) framesNotLeader(tracer *trace.Tracer, txn *transaction.Txn) error {
	if txn.State() == transaction.Pending {
		// No Frames command was applied, so followers don't
		// know about this transaction. We don't need to do
		// anything special, the xUndo hook will just remove
		// it.
		tracer.Message("no frames command applied")
	} else {
		// At least one Frames command was applied, so the transaction
		// exists on the followers. We mark the transaction as zombie,
		// the Begin() hook of next leader (either us or somebody else)
		// will detect a dangling transaction and issue an Undo command
		// to roll it back. In its applyUndo method our FSM will detect
		// that the rollback is for a zombie and just no-op it.
		tracer.Message("marking as zombie")
		txn.Zombie()
	}

	// When we return an error, SQLite will fire the End hook.
	tracer.Message("not leader")

	return bindings.Error{Code: bindings.ErrIoErrNotLeader}
}

// Undo is the hook invoked by sqlite when a write transaction needs
// to be rolled back.
func (m *Methods) Undo(conn *bindings.Conn) int {
	// We take a the lock for the entire duration of the hook to avoid
	// races between to cocurrent hooks.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Lock the registry.
	m.registry.Lock()
	defer m.registry.Unlock()

	// Enable synchronization with the FSM: it will only execute commands
	// applied during this hook, and block applying any other command until
	// this hook is done.
	m.registry.HookSyncSet()
	defer m.registry.HookSyncReset()

	tracer := m.registry.TracerConn(conn, "undo")
	tracer.Message("start")

	txn := m.registry.TxnByConn(conn)
	if txn == nil {
		tracer.Panic("no in-progress transaction")
	}
	tracer.Message("found txn %s", txn)

	// Sanity check.
	if !txn.IsLeader() {
		tracer.Panic("unexpected transaction %s", txn)
	}

	if txn.IsZombie() {
		// This zombie originated from the Frames hook. There are two scenarios:
		//
		// 1. Leadership was lost while applying the Frames command.
		//
		//    We can't simply remove the transaction since the Frames
		//    command might eventually get committed. We just ignore
		//    it, and let it handle by our FSM in that case (i.e. if we
		//    are re-elected or a quorum was reached and another leader
		//    tries to apply it).
		//
		// 2. This server was not the leader anymore when the Frames
		//    hook fired for a commit frames batch which was the last
		//    of a sequence of non-commit ones.
		//
		//    In this case we're being invoked by SQLite which is
		//    trying to rollback the transaction. We can't simply
		//    remove the transaction since the next leader will detect
		//    a dangling transaction and try to issue an Undo
		//    command. We just ignore the zombie and let our FSM handle
		//    it when the Undo command will be applied.
		tracer.Message("done: ignore zombie")
		return 0
	}

	if txn.State() == transaction.Pending {
		// This means that the Undo hook fired because this node was
		// not the leader when trying to apply the first Frames
		// command, so no follower knows about it. We can just return,
		// the transaction will be removed by the End hook.
		tracer.Message("done: no frames command was sent")
		return 0
	}

	// Check if we're the leader.
	if m.noLeaderCheck == 0 && m.raft.State() != raft.Leader {
		// If we have lost leadership we're in a state where the
		// transaction began on this node and a quorum of followers. We
		// return an error, and SQLite will ignore it, however we
		// need to create a surrogate follower, so the next leader will try to
		// undo it across all nodes.
		tracer.Message("not leader")
		m.surrogateWriting(tracer, txn)
		return bindings.ErrIoErrNotLeader
	}

	// Update the noLeaderCheck counter.
	if m.noLeaderCheck > 0 {
		m.noLeaderCheck--
	}

	// We don't really care whether the Undo command applied just below here
	// will be committed or not.If the command fails, we'll create a
	// surrogate follower: if the command still gets committed, then the
	// rollback succeeds and the next leader will start fresh, if if the
	// command does not get committed, the next leader will find a stale
	// follower and re-try to roll it back.
	if txn.State() != transaction.Pending {
		command := protocol.NewUndo(txn.ID())
		if err := m.apply(tracer, conn, command); err != nil {
			m.surrogateWriting(tracer, txn)
			return errno(err)
		}
	}

	tracer.Message("done")

	return 0
}

// End is the hook invoked by sqlite when ending a write transaction.
func (m *Methods) End(conn *bindings.Conn) int {
	// We take a the lock for the entire duration of the hook to avoid
	// races between to cocurrent hooks.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Lock the registry.
	m.registry.Lock()
	defer m.registry.Unlock()

	tracer := m.registry.TracerConn(conn, "end")
	tracer.Message("start")

	txn := m.registry.TxnByConn(conn)
	if txn == nil {
		// Check if we have a surrogate transaction instead.
		filename := m.registry.ConnLeaderFilename(conn)
		conn := m.registry.ConnFollower(filename)
		txn = m.registry.TxnByConn(conn)
		if txn == nil {
			// Ignore missing transactions that might have been removed by a
			// particularly bad timing where a new leader has already sent
			// some Undo command following a leadership change and our FSM
			// applied it against a surrogate, removing it from the
			// registry.
			tracer.Message("done: ignore missing transaction")
			return 0
		}
	} else {
		// Sanity check
		if txn.Conn() != conn {
			tracer.Panic("unexpected transaction", conn)
		}
	}
	tracer.Message("found txn %s", txn)

	if !txn.IsLeader() {
		// This must be a surrogate follower created by the Frames or
		// Undo hooks. Let's ignore it, has it will be handled by the
		// next leader of FSM.
		tracer.Message("done: ignore surrogate follower")
		return 0
	}

	if txn.IsZombie() {
		// Ignore zombie transactions as we don't know what will happen
		// to them (either committed or not).
		tracer.Message("done: ignore zombie")
		return 0
	}

	tracer.Message("unregister transaction")
	m.registry.TxnDel(txn.ID())

	tracer.Message("done")

	return 0
}

// Create a surrogate follower transaction, transiting it to the Writing state.
func (m *Methods) surrogateWriting(tracer *trace.Tracer, txn *transaction.Txn) {
	tracer.Message("surrogate to Writing")
	// TODO: fix
	//txn = m.registry.TxnFollowerSurrogate(txn)
	//txn.Frames(true, &sqlite3.ReplicationFramesParams{IsCommit: 0})
}

// Apply the given command through raft.
func (m *Methods) apply(tracer *trace.Tracer, conn *bindings.Conn, cmd *protocol.Command) error {
	tracer = tracer.With(trace.String("cmd", cmd.Name()))
	tracer.Message("apply start")

	data, err := protocol.MarshalCommand(cmd)
	if err != nil {
		return err
	}

	// We need to release the lock while the command is being applied,
	// since the FSM of this raft instance needs to be able to acquire
	// it. However, since we don't want the FSM to execute more than one
	// log we also configure the registry's HookSync so the FSM will block
	// on executing any log command otherwise than the one we are sending
	// now. See also internal/registry/hook.go.
	m.registry.HookSyncAdd(data)
	m.registry.Unlock()
	err = m.raft.Apply(data, m.applyTimeout).Error()
	m.registry.Lock()

	if err != nil {
		tracer.Error("apply error", err)

		// If the server has lost leadership or is shutting down, we
		// return a dedicated error, so clients will typically retry
		// against the new leader.
		switch err {
		case raft.ErrRaftShutdown:
			// For our purposes, this is semantically equivalent
			// to not being the leader anymore.
			fallthrough
		case raft.ErrNotLeader:
			return bindings.Error{Code: bindings.ErrIoErrNotLeader}
		case raft.ErrLeadershipLost:
			return bindings.Error{Code: bindings.ErrIoErrLeadershipLost}
		case raft.ErrEnqueueTimeout:
			// This should be pretty much impossible, since Methods
			// hooks are the only way to apply command logs, and
			// hooks always wait for a command log to finish before
			// applying a new one (see the Apply().Error() call
			// above). We return SQLITE_INTERRUPT, which for our
			// purposes has the same semantics as SQLITE_IOERR,
			// i.e. it will automatically rollback the transaction.
			return bindings.Error{Code: bindings.ErrInterrupt}
		default:
			// This is an unexpected raft error of some kind.
			//
			// TODO: We should investigate what this could be,
			//       for example how to properly handle ErrAbortedByRestore
			//       or log-store related errors. We should also
			//       examine what SQLite exactly does if we return
			//       SQLITE_INTERNAL.
			return bindings.Error{Code: bindings.ErrInternal}
		}

	}

	tracer.Message("apply done")
	return nil
}

// Convert a Go error into a SQLite error number.
func errno(err error) int {
	switch e := err.(type) {
	case bindings.Error:
		return e.Code
	default:
		return bindings.ErrInternal
	}
}

func isErrNotLeader(err error) bool {
	if err, ok := err.(bindings.Error); ok {
		if err.Code == bindings.ErrIoErrNotLeader {
			return true
		}
	}
	return false
}

func isErrLeadershipLost(err error) bool {
	if err, ok := err.(bindings.Error); ok {
		if err.Code == bindings.ErrIoErrLeadershipLost {
			return true
		}
	}
	return false
}
