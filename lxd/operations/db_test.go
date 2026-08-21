package operations

import (
	"maps"
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

func (s *dbSuite) Test_filterReconstructCandidatesForFinalization() {
	now := time.Now()
	type testCase struct {
		name                             string
		operationsToReconstruct          map[string]cluster.Operation
		operationIDRetentionSet          map[int64]struct{}
		durableOperationUUIDsToRestart   map[string]struct{}
		expectedIDs                      []int64
		expectedReconstructOpsStatusCode map[string]int64
		expectedReconstructOpsUpdatedAt  map[string]time.Time
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
			operationIDRetentionSet:          map[int64]struct{}{},
			durableOperationUUIDsToRestart:   map[string]struct{}{},
			expectedIDs:                      []int64{},
			expectedReconstructOpsStatusCode: map[string]int64{"uuid-1": int64(api.Success)},
			expectedReconstructOpsUpdatedAt:  map[string]time.Time{"uuid-1": now.Add(-1 * time.Second)},
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
			operationIDRetentionSet:          map[int64]struct{}{},
			durableOperationUUIDsToRestart:   map[string]struct{}{},
			expectedIDs:                      []int64{1},
			expectedReconstructOpsStatusCode: map[string]int64{"uuid-1": danglingOperationFinalizationStatusCode},
			expectedReconstructOpsUpdatedAt:  map[string]time.Time{"uuid-1": now},
		},
		{
			name: "skip child operation with parent not in retention set",
			operationsToReconstruct: map[string]cluster.Operation{
				"uuid-parent": {
					Row: cluster.OperationsRow{
						ID:         1,
						UUID:       "uuid-parent",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
					},
				},
				"uuid-child": {
					Row: cluster.OperationsRow{
						ID:         2,
						UUID:       "uuid-child",
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
						Parent:     func() *int64 { i := int64(1); return &i }(),
					},
				},
			},
			operationIDRetentionSet:          map[int64]struct{}{},
			durableOperationUUIDsToRestart:   map[string]struct{}{},
			expectedIDs:                      []int64{1},
			expectedReconstructOpsStatusCode: map[string]int64{"uuid-parent": danglingOperationFinalizationStatusCode},
			expectedReconstructOpsUpdatedAt:  map[string]time.Time{"uuid-parent": now},
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
						Parent:     func() *int64 { i := int64(1); return &i }(),
					},
				},
			},
			operationIDRetentionSet:          map[int64]struct{}{1: {}},
			durableOperationUUIDsToRestart:   map[string]struct{}{},
			expectedIDs:                      []int64{2},
			expectedReconstructOpsStatusCode: map[string]int64{"uuid-child": danglingOperationFinalizationStatusCode},
			expectedReconstructOpsUpdatedAt:  map[string]time.Time{"uuid-child": now},
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
			operationIDRetentionSet:          map[int64]struct{}{},
			durableOperationUUIDsToRestart:   map[string]struct{}{"uuid-durable": {}},
			expectedIDs:                      []int64{},
			expectedReconstructOpsStatusCode: map[string]int64{"uuid-durable": int64(api.Running)},
			expectedReconstructOpsUpdatedAt:  map[string]time.Time{"uuid-durable": now.Add(-1 * time.Second)},
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
			expectedReconstructOpsStatusCode: map[string]int64{
				"uuid-1": danglingOperationFinalizationStatusCode,
				"uuid-2": danglingOperationFinalizationStatusCode,
				"uuid-3": danglingOperationFinalizationStatusCode,
			},
			expectedReconstructOpsUpdatedAt: map[string]time.Time{
				"uuid-1": now,
				"uuid-2": now,
				"uuid-3": now,
			},
		},
		{
			name: "mixed operations with various conditions",
			operationsToReconstruct: map[string]cluster.Operation{
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
						StatusCode: int64(api.Running),
						UpdatedAt:  now.Add(-1 * time.Second),
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
			expectedReconstructOpsStatusCode: map[string]int64{
				"uuid-final":           int64(api.Success),
				"uuid-running":         danglingOperationFinalizationStatusCode,
				"uuid-durable":         int64(api.Running),
				"uuid-child-no-parent": int64(api.Running),
			},
			expectedReconstructOpsUpdatedAt: map[string]time.Time{
				"uuid-final":           now.Add(-1 * time.Second),
				"uuid-running":         now,
				"uuid-durable":         now.Add(-1 * time.Second),
				"uuid-child-no-parent": now.Add(-1 * time.Second),
			},
		},
	}

	for i, tt := range tests {
		s.T().Logf("case %d: %q", i, tt.name)

		// Make a copy of the map for inspection.
		originalOperationsToReconstruct := maps.Clone(tt.operationsToReconstruct)
		actualIDs := filterReconstructCandidatesForFinalization(
			now,
			tt.operationsToReconstruct,
			tt.operationIDRetentionSet,
			tt.durableOperationUUIDsToRestart,
		)

		s.ElementsMatch(tt.expectedIDs, actualIDs, "case %d: operation IDs mismatch", i)

		// Verify that operations in the map have expected status codes and updated times.
		for uuid, expectedStatusCode := range tt.expectedReconstructOpsStatusCode {
			op, ok := tt.operationsToReconstruct[uuid]
			if !ok {
				// Operation was removed from the map (child with parent not in retention set).
				s.Nil(originalOperationsToReconstruct[uuid].Row.Parent, "case %d: operation %q should not have been removed", i, uuid)
				continue
			}

			s.Equal(expectedStatusCode, op.Row.StatusCode, "case %d: operation %q status code mismatch", i, uuid)
			s.Equal(tt.expectedReconstructOpsUpdatedAt[uuid], op.Row.UpdatedAt, "case %d: operation %q updated at mismatch", i, uuid)

			// Verify error fields for finalized operations.
			if expectedStatusCode == danglingOperationFinalizationStatusCode {
				s.Equal(danglingOperationFinalizationErrorText, op.Row.Error, "case %d: operation %q error text mismatch", i, uuid)
				s.Equal(danglingOperationFinalizationErrorCode, op.Row.ErrorCode, "case %d: operation %q error code mismatch", i, uuid)
			}
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
