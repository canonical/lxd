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

	timePtr := func(t time.Time) *time.Time {
		return &t
	}

	tests := []testCase{
		{
			name: "running operation updated 4 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.updatedAt.Store(timePtr(now.Add(-4 * time.Second)))
				return op
			},
			expected: true,
		},
		{
			name: "running operation updated 6 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.updatedAt.Store(timePtr(now.Add(-6 * time.Second)))
				return op
			},
			expected: true,
		},
		{
			name: "finished operation updated 4 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.finished.Cancel()
				op.updatedAt.Store(timePtr(now.Add(-4 * time.Second)))
				return op
			},
			expected: true,
		},
		{
			name: "finished operation updated 6 seconds ago",
			opFunc: func() *Operation {
				op := newTestOp(s.Require())
				op.finished.Cancel()
				op.updatedAt.Store(timePtr(now.Add(-6 * time.Second)))
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
				parent.updatedAt.Store(timePtr(time.Now().Add(-(24*time.Hour + time.Second))))
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
				child.updatedAt.Store(timePtr(time.Now().Add(-(24*time.Hour + time.Second))))
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
				parent.updatedAt.Store(timePtr(now.Add(-(24*time.Hour - time.Second))))
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
				child.updatedAt.Store(timePtr(now.Add(-(24*time.Hour - time.Second))))
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
				parent.updatedAt.Store(timePtr(now.Add(-(24*time.Hour + time.Second))))
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

func (s *dbSuite) Test_filterReconstructCandidatesForFinalization() {
	now := time.Now()
	type testCase struct {
		name                            string
		operationsToReconstruct         map[string]cluster.Operation
		operationIDRetentionSet         map[int64]struct{}
		durableOperationUUIDsToRestart  map[string]struct{}
		expectedIDs                     []int64
		expectedOperationsToReconstruct map[string]cluster.Operation
	}

	int64Ptr := func(i int64) *int64 {
		return &i
	}

	tests := []testCase{
		{
			name: "skip operation with final status",
			operationsToReconstruct: map[string]cluster.Operation{
				"uuid-1": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-1",
						StatusCode: int64(api.Success),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
			},
			operationIDRetentionSet:        map[int64]struct{}{},
			durableOperationUUIDsToRestart: map[string]struct{}{},
			expectedIDs:                    []int64{},
			expectedOperationsToReconstruct: map[string]cluster.Operation{
				"uuid-1": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-1",
						StatusCode: int64(api.Success),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
			},
		},
		{
			name: "finalize non-final operation",
			operationsToReconstruct: map[string]cluster.Operation{
				"uuid-1": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-1",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
			},
			operationIDRetentionSet:        map[int64]struct{}{},
			durableOperationUUIDsToRestart: map[string]struct{}{},
			expectedIDs:                    []int64{1},
			expectedOperationsToReconstruct: map[string]cluster.Operation{
				"uuid-1": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-1",
						StatusCode: danglingOperationFinalizationStatusCode,
						UpdatedAt:  now,
						Error:      danglingOperationFinalizationErrorText,
						ErrorCode:  danglingOperationFinalizationErrorCode,
					},
				},
			},
		},
		{
			name: "skip child operation with parent not in retention set",
			operationsToReconstruct: map[string]cluster.Operation{
				"uuid-child": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-child",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
						Parent:     int64Ptr(1),
					},
				},
			},
			operationIDRetentionSet:         map[int64]struct{}{},
			durableOperationUUIDsToRestart:  map[string]struct{}{},
			expectedIDs:                     []int64{},
			expectedOperationsToReconstruct: map[string]cluster.Operation{},
		},
		{
			name: "finalize child operation with parent in retention set",
			operationsToReconstruct: map[string]cluster.Operation{
				"uuid-parent": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-parent",
						StatusCode: int64(api.Success),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				"uuid-child": {
					Row: cluster.OperationsRow{
						ID:         2,
						UUID:       "uuid-child",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
						Parent:     int64Ptr(1),
					},
				},
			},
			operationIDRetentionSet:        map[int64]struct{}{1: {}},
			durableOperationUUIDsToRestart: map[string]struct{}{},
			expectedIDs:                    []int64{2},
			expectedOperationsToReconstruct: map[string]cluster.Operation{
				"uuid-parent": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-parent",
						StatusCode: int64(api.Success),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				"uuid-child": {
					Row: cluster.OperationsRow{
						ID:         2,
						UUID:       "uuid-child",
						StatusCode: danglingOperationFinalizationStatusCode,
						UpdatedAt:  now,
						ErrorCode:  danglingOperationFinalizationErrorCode,
						Error:      danglingOperationFinalizationErrorText,
						Parent:     int64Ptr(1),
					},
				},
			},
		},
		{
			name: "skip durable operation to be restarted",
			operationsToReconstruct: map[string]cluster.Operation{
				"uuid-durable": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-durable",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
			},
			operationIDRetentionSet:        map[int64]struct{}{},
			durableOperationUUIDsToRestart: map[string]struct{}{"uuid-durable": {}},
			expectedIDs:                    []int64{},
			expectedOperationsToReconstruct: map[string]cluster.Operation{
				"uuid-durable": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-durable",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
			},
		},
		{
			name: "finalize multiple operations",
			operationsToReconstruct: map[string]cluster.Operation{
				"uuid-1": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-1",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				"uuid-2": {
					Row: cluster.OperationsRow{
						ID:         2,
						UUID:       "uuid-2",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-2 * time.Second),
					},
				},
				"uuid-3": {
					Row: cluster.OperationsRow{
						ID:         3,
						UUID:       "uuid-3",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-3 * time.Second),
					},
				},
			},
			operationIDRetentionSet:        map[int64]struct{}{},
			durableOperationUUIDsToRestart: map[string]struct{}{},
			expectedIDs:                    []int64{1, 2, 3},
			expectedOperationsToReconstruct: map[string]cluster.Operation{
				"uuid-1": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-1",
						UpdatedAt:  now,
						StatusCode: danglingOperationFinalizationStatusCode,
						ErrorCode:  danglingOperationFinalizationErrorCode,
						Error:      danglingOperationFinalizationErrorText,
					},
				},
				"uuid-2": {
					Row: cluster.OperationsRow{
						ID:         2,
						UUID:       "uuid-2",
						UpdatedAt:  now,
						StatusCode: danglingOperationFinalizationStatusCode,
						ErrorCode:  danglingOperationFinalizationErrorCode,
						Error:      danglingOperationFinalizationErrorText,
					},
				},
				"uuid-3": {
					Row: cluster.OperationsRow{
						ID:         3,
						UUID:       "uuid-3",
						UpdatedAt:  now,
						StatusCode: danglingOperationFinalizationStatusCode,
						ErrorCode:  danglingOperationFinalizationErrorCode,
						Error:      danglingOperationFinalizationErrorText,
					},
				},
			},
		},
		{
			name: "mixed operations with various conditions",
			operationsToReconstruct: map[string]cluster.Operation{
				// To be unmodified.
				"uuid-final": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-final",
						StatusCode: int64(api.Success),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				// To be finalized. The ID should be returned.
				"uuid-running": {
					Row: cluster.OperationsRow{
						ID:         2,
						UUID:       "uuid-running",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				// To be unmodified (it will be restarted)
				"uuid-durable": {
					Row: cluster.OperationsRow{
						ID:         3,
						UUID:       "uuid-durable",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				// To be removed from the reconstruct set.
				"uuid-child-no-parent": {
					Row: cluster.OperationsRow{
						ID:         4,
						UUID:       "uuid-child-no-parent",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
						Parent:     func() *int64 { i := int64(99); return &i }(),
					},
				},
			},
			operationIDRetentionSet:        map[int64]struct{}{},
			durableOperationUUIDsToRestart: map[string]struct{}{"uuid-durable": {}},
			expectedIDs:                    []int64{2},
			expectedOperationsToReconstruct: map[string]cluster.Operation{
				"uuid-final": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-final",
						StatusCode: int64(api.Success),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				"uuid-running": {
					Row: cluster.OperationsRow{
						ID:         2,
						UUID:       "uuid-running",
						UpdatedAt:  now,
						StatusCode: danglingOperationFinalizationStatusCode,
						ErrorCode:  danglingOperationFinalizationErrorCode,
						Error:      danglingOperationFinalizationErrorText,
					},
				},
				"uuid-durable": {
					Row: cluster.OperationsRow{
						ID:         3,
						UUID:       "uuid-durable",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
			},
		},
	}

	for i, tt := range tests {
		s.T().Logf("case %d: %q", i, tt.name)

		// Make a copy of the map for inspection.
		actualIDs := filterReconstructCandidatesForFinalization(
			now,
			tt.operationsToReconstruct,
			tt.operationIDRetentionSet,
			tt.durableOperationUUIDsToRestart,
		)

		s.ElementsMatch(tt.expectedIDs, actualIDs, "case %d: operation IDs mismatch", i)
		s.Len(tt.operationsToReconstruct, len(tt.expectedOperationsToReconstruct))

		for opUUID, expectedOpToReconstruct := range tt.expectedOperationsToReconstruct {
			op, ok := tt.operationsToReconstruct[opUUID]
			if !ok {
				s.Failf("Operation missing from reconstructed operations map", "case %d: Missing UUID %q", i, opUUID)
			}

			s.Equal(op.Row.StatusCode, expectedOpToReconstruct.Row.StatusCode)
			s.Equal(op.Row.Error, expectedOpToReconstruct.Row.Error)
			s.Equal(op.Row.ErrorCode, expectedOpToReconstruct.Row.ErrorCode)
			s.Equal(op.Row.UpdatedAt, expectedOpToReconstruct.Row.UpdatedAt)
		}
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
