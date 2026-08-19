package operations

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

type operationsSuite struct {
	suite.Suite
}

func newTestOp(require *require.Assertions) *Operation {
	v7UUID, err := uuid.NewV7()
	require.NoError(err)
	opUUID := v7UUID.String()
	sec, nsec := v7UUID.Time().UnixTime()
	now := time.Unix(sec, nsec)

	op := &Operation{
		id:                     opUUID,
		class:                  1,
		createdAt:              now,
		updatedAt:              now,
		status:                 api.Running,
		url:                    "/1.0",
		entityURL:              entity.ServerURL(),
		dbOpType:               1000,
		logger:                 logger.AddContext(logger.Ctx{"uuid": opUUID}),
		finished:               cancel.New(),
		running:                cancel.New(),
		lastPersistenceAttempt: now,
		readonly:               false,
	}

	setDoneFunc(op)

	return op
}

func TestOperationsSuite(t *testing.T) {
	suite.Run(t, new(operationsSuite))
}

// withLock wraps operation map locking for testing so that it fails immediately if locked
// (rather than waiting for the test deadline).
func (s *operationsSuite) withLock(f func()) {
	wasUnlocked := operationsLock.TryLock()
	if !wasUnlocked {
		s.Require().FailNow("Operations map is locked")
	}

	f()

	operationsLock.Unlock()
}

func (s *operationsSuite) SetupTest() {
	// Ensures a blank operations map for each test.
	s.withLock(func() {
		operations = make(map[string]*Operation)
	})
}

func (s *operationsSuite) TearDownTest() {
	// Ensures operationsLock is not locked when the test is torn down.
	s.withLock(func() {})
}

func (s *operationsSuite) Test_deleteInternalNoChildren() {
	op1 := newTestOp(s.Require())
	op2 := newTestOp(s.Require())
	op3 := newTestOp(s.Require())

	s.withLock(func() {
		operations[op1.id] = op1
		operations[op2.id] = op2
		operations[op3.id] = op3
	})

	notFoundUUIDv7, err := uuid.NewV7()
	s.Require().NoError(err)
	deleteInternal(op1.id, op3.id, notFoundUUIDv7.String())

	s.withLock(func() {
		s.Len(operations, 1)
		_, ok := operations[op2.id]
		s.True(ok)
	})

	s.True(op1.readonly)
	s.ErrorIs(op1.finished.Err(), context.Canceled)
	s.True(op3.readonly)
	s.ErrorIs(op3.finished.Err(), context.Canceled)
	s.False(op2.readonly)
	s.NoError(op2.finished.Err())
}

func (s *operationsSuite) Test_deleteInternalParentWithChildren() {
	op1 := newTestOp(s.Require())
	op2 := newTestOp(s.Require())
	op1Child := newTestOp(s.Require())
	op1Child.parent = op1
	op1.children = []*Operation{op1Child}

	s.withLock(func() {
		operations[op1.id] = op1
		operations[op2.id] = op2
		operations[op1Child.id] = op1Child
	})

	// Deleting the child operation is a no-op.
	deleteInternal(op1Child.id)

	s.withLock(func() {
		s.Len(operations, 3)
	})

	s.False(op1.readonly)
	s.NoError(op1.finished.Err())
	s.False(op1Child.readonly)
	s.NoError(op1Child.finished.Err())
	s.False(op2.readonly)
	s.NoError(op2.finished.Err())

	deleteInternal(op1.id)

	s.withLock(func() {
		s.Len(operations, 1)
		_, ok := operations[op2.id]
		s.True(ok)
	})

	s.True(op1.readonly)
	s.ErrorIs(op1.finished.Err(), context.Canceled)
	s.True(op1Child.readonly)
	s.ErrorIs(op1Child.finished.Err(), context.Canceled)
	s.False(op2.readonly)
	s.NoError(op2.finished.Err())
}
