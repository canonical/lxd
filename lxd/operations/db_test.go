package operations

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared/api"
)

type dbSuite struct {
	suite.Suite
}

func TestDB(t *testing.T) {
	suite.Run(t, new(dbSuite))
}

func (s *dbSuite) Test_isRetentionCandidateLocal() {
	now := time.Now()
	type testCase struct {
		name     string
		opFunc   func() *Operation
		expected bool
	}

	tests := []testCase{
		{
			name: "running operation updated 4 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.updatedAt = now.Add(-4 * time.Second)
				return op
			},
			expected: true,
		},
		{
			name: "running operation updated 6 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.updatedAt = now.Add(-6 * time.Second)
				return op
			},
			expected: true,
		},
		{
			name: "finished operation updated 4 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.finished.Cancel()
				op.updatedAt = now.Add(-4 * time.Second)
				return op
			},
			expected: true,
		},
		{
			name: "finished operation updated 6 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.finished.Cancel()
				op.updatedAt = now.Add(-6 * time.Second)
				return op
			},
			expected: false,
		},
		{
			name: "running bulk operation parent updated 24 hours and 1 second ago",
			opFunc: func() *Operation {
				parent := newTestOp(s.Require())
				child := newTestOp(s.Require())
				parent.children = []*Operation{child}
				parent.updatedAt = time.Now().Add(-(24*time.Hour + time.Second))
				return parent
			},
			expected: true,
		},
		{
			name: "running bulk operation child updated 24 hours and 1 second ago",
			opFunc: func() *Operation {
				parent := newTestOp(s.Require())
				child := newTestOp(s.Require())
				parent.children = []*Operation{child}
				child.parent = parent
				child.updatedAt = time.Now().Add(-(24*time.Hour + time.Second))
				return child
			},
			expected: true,
		},
		{
			name: "finished bulk operation parent updated 23 hours 59 minutes and 59 seconds ago",
			opFunc: func() *Operation {
				parent := newTestOp(s.Require())
				child := newTestOp(s.Require())
				parent.children = []*Operation{child}
				child.parent = parent
				parent.finished.Cancel()
				parent.updatedAt = now.Add(-(24*time.Hour - time.Second))
				return parent
			},
			expected: true,
		},
		{
			name: "finished bulk operation child updated 23 hours 59 minutes and 59 seconds ago",
			opFunc: func() *Operation {
				parent := newTestOp(s.Require())
				child := newTestOp(s.Require())
				parent.children = []*Operation{child}
				child.parent = parent
				child.finished.Cancel()
				child.updatedAt = now.Add(-(24*time.Hour - time.Second))
				return child
			},
			expected: true,
		},
		{
			name: "finished bulk operation parent updated 24 hours and 1 second ago",
			opFunc: func() *Operation {
				parent := newTestOp(s.Require())
				child := newTestOp(s.Require())
				parent.children = []*Operation{child}
				child.parent = parent
				parent.finished.Cancel()
				parent.updatedAt = now.Add(-(24*time.Hour + time.Second))
				return parent
			},
			expected: false,
		},
	}

	for i, tt := range tests {
		s.T().Logf("case %d: %q", i, tt.name)
		op := tt.opFunc()
		actual := isRetentionCandidate(now, op, len(op.children) > 0)
		s.Equal(tt.expected, actual)
	}
}

func (s *dbSuite) Test_isRetentionCandidateDB() {
	now := time.Now()
	type testCase struct {
		name     string
		opFunc   func() cluster.Operation
		isParent bool
		expected bool
	}

	tests := []testCase{
		{
			name: "running operation updated 4 seconds ago",
			opFunc: func() cluster.Operation {
				op := newTestDBOp(s.Require())
				op.Row.UpdatedAt = now.Add(-4 * time.Second)
				return op
			},
			expected: true,
		},
		{
			name: "running operation updated 6 seconds ago",
			opFunc: func() cluster.Operation {
				op := newTestDBOp(s.Require())
				op.Row.UpdatedAt = now.Add(-6 * time.Second)
				return op
			},
			expected: true,
		},
		{
			name: "finished operation updated 4 seconds ago",
			opFunc: func() cluster.Operation {
				op := newTestDBOp(s.Require())
				op.Row.StatusCode = int64(api.Success)
				op.Row.UpdatedAt = now.Add(-4 * time.Second)
				return op
			},
			expected: true,
		},
		{
			name: "finished operation updated 6 seconds ago",
			opFunc: func() cluster.Operation {
				op := newTestDBOp(s.Require())
				op.Row.StatusCode = int64(api.Success)
				op.Row.UpdatedAt = now.Add(-6 * time.Second)
				return op
			},
			expected: false,
		},
		{
			name: "running bulk operation parent updated 24 hours and 1 second ago",
			opFunc: func() cluster.Operation {
				parent := newTestDBOp(s.Require())
				parent.Row.UpdatedAt = time.Now().Add(-(24*time.Hour + time.Second))
				return parent
			},
			isParent: true,
			expected: true,
		},
		{
			name: "running bulk operation child updated 24 hours and 1 second ago",
			opFunc: func() cluster.Operation {
				parent := newTestDBOp(s.Require())
				child := newTestDBOp(s.Require())
				child.Row.UpdatedAt = time.Now().Add(-(24*time.Hour + time.Second))
				child.Row.Parent = &parent.Row.ID
				return child
			},
			expected: true,
		},
		{
			name: "finished bulk operation parent updated 23 hours 59 minutes and 59 seconds ago",
			opFunc: func() cluster.Operation {
				parent := newTestDBOp(s.Require())
				parent.Row.StatusCode = int64(api.Success)
				parent.Row.UpdatedAt = now.Add(-(24*time.Hour - time.Second))
				return parent
			},
			isParent: true,
			expected: true,
		},
		{
			name: "finished bulk operation child updated 23 hours 59 minutes and 59 seconds ago",
			opFunc: func() cluster.Operation {
				parent := newTestDBOp(s.Require())
				child := newTestDBOp(s.Require())
				child.Row.StatusCode = int64(api.Success)
				child.Row.UpdatedAt = now.Add(-(24*time.Hour - time.Second))
				child.Row.Parent = &parent.Row.ID
				return child
			},
			expected: true,
		},
		{
			name: "finished bulk operation parent updated 24 hours and 1 second ago",
			opFunc: func() cluster.Operation {
				parent := newTestDBOp(s.Require())
				parent.Row.StatusCode = int64(api.Success)
				parent.Row.UpdatedAt = now.Add(-(24*time.Hour + time.Second))
				return parent
			},
			isParent: true,
			expected: false,
		},
	}

	for i, tt := range tests {
		s.T().Logf("case %d: %q", i, tt.name)
		op := tt.opFunc()
		actual := isRetentionCandidate(now, op, tt.isParent)
		s.Equal(tt.expected, actual)
	}
}

var dbID int64

func newTestDBOp(require *require.Assertions) cluster.Operation {
	v7UUID, err := uuid.NewV7()
	require.NoError(err)
	opUUID := v7UUID.String()
	sec, nsec := v7UUID.Time().UnixTime()
	now := time.Unix(sec, nsec)

	dbID++
	return cluster.Operation{
		Row: cluster.OperationsRow{
			ID:         dbID,
			UUID:       opUUID,
			NodeID:     1,
			Metadata:   "{}",
			Class:      1,
			CreatedAt:  now,
			UpdatedAt:  now,
			StatusCode: int64(api.Running),
		},
	}
}
