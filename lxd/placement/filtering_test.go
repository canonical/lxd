package placement

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
)

type filteringSuite struct {
	suite.Suite
}

func TestFilteringSuite(t *testing.T) {
	suite.Run(t, new(filteringSuite))
}

func (s *filteringSuite) TestFilter() {
	testCluster, cleanup := db.NewTestCluster(s.T())
	defer cleanup()

	// Create 5 candidate cluster members.
	nodeNames := []string{"member01", "member02", "member03", "member04", "member05"}

	candidates := make([]db.NodeInfo, 0, len(nodeNames))
	for i, nodeName := range nodeNames {
		candidates = append(candidates, db.NodeInfo{Name: nodeName, Address: fmt.Sprintf("192.0.2.%d", i)})
	}

	candidatesWithout := func(members ...string) []db.NodeInfo {
		filteredCandidates := make([]db.NodeInfo, 0, len(candidates))
		for _, candidate := range candidates {
			if !slices.Contains(members, candidate.Name) {
				filteredCandidates = append(filteredCandidates, candidate)
			}
		}

		return filteredCandidates
	}

	candidatesOnly := func(members ...string) []db.NodeInfo {
		filteredCandidates := make([]db.NodeInfo, 0, len(members))
		for _, candidate := range candidates {
			if slices.Contains(members, candidate.Name) {
				filteredCandidates = append(filteredCandidates, candidate)
			}
		}

		return filteredCandidates
	}

	err := testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		for i, node := range candidates {
			id, err := tx.CreateNode(node.Name, node.Address)
			candidates[i].ID = id
			s.Require().NoError(err)
		}

		return nil
	})
	s.Require().NoError(err)

	type args struct {
		candidates     []db.NodeInfo
		project        string
		placementGroup cluster.PlacementGroup
	}

	tests := []struct {
		name         string
		args         args
		caseSetup    func()
		caseTearDown func()
		want         []db.NodeInfo
		wantErr      bool
	}{
		// Policy: spread
		// Rigor: strict
		{
			name: "spread/strict: initial placement (no instances yet)",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Create placement group with spread/strict policy.
					pgID, err := cluster.CreatePlacementGroup(ctx, tx.Tx(), cluster.PlacementGroup{
						Project:     "default",
						Name:        "pg-spread-strict",
						Description: "Spread strict placement group",
					})
					s.Require().NoError(err)

					err = cluster.CreatePlacementGroupConfig(ctx, tx.Tx(), pgID, map[string]string{
						"policy": api.PlacementPolicySpread,
						"rigor":  api.PlacementRigorStrict,
					})
					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-spread-strict",
					Description: "Spread strict placement group",
				},
			},
			want:    candidates, // All candidates available for first instance.
			wantErr: false,
		},
		{
			name: "spread/strict: second instance (one member occupied)",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Create instance on member01.
					instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
						Name:    "c1",
						Node:    "member01",
						Project: "default",
						Type:    instancetype.Container,
					})
					s.Require().NoError(err)

					err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
						"placement.group": "pg-spread-strict",
					})
					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-spread-strict",
					Description: "Spread strict placement group",
				},
			},
			want:    candidatesWithout("member01"), // member01 has instance, exclude it.
			wantErr: false,
		},
		{
			name: "spread/strict: all members occupied should return error",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Add instances to all remaining members.
					for i, nodeName := range []string{"member02", "member03", "member04", "member05"} {
						instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
							Name:    fmt.Sprintf("c%d", i+2),
							Node:    nodeName,
							Project: "default",
							Type:    instancetype.Container,
						})
						s.Require().NoError(err)

						err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
							"placement.group": "pg-spread-strict",
						})
						s.Require().NoError(err)
					}

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-spread-strict",
					Description: "Spread strict placement group",
				},
			},
			want:    nil,
			wantErr: true, // All members occupied, should fail.
			caseTearDown: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Delete all instances.
					for i := 1; i <= 5; i++ {
						_ = cluster.DeleteInstance(ctx, tx.Tx(), "default", fmt.Sprintf("c%d", i))
					}

					return nil
				})
			},
		},

		// Policy: spread
		// Rigor: permissive
		{
			name: "spread/permissive: initial placement",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					pgID, err := cluster.CreatePlacementGroup(ctx, tx.Tx(), cluster.PlacementGroup{
						Project:     "default",
						Name:        "pg-spread-permissive",
						Description: "Spread permissive placement group",
					})
					s.Require().NoError(err)

					err = cluster.CreatePlacementGroupConfig(ctx, tx.Tx(), pgID, map[string]string{
						"policy": api.PlacementPolicySpread,
						"rigor":  api.PlacementRigorPermissive,
					})
					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-spread-permissive",
					Description: "Spread permissive placement group",
				},
			},
			want:    candidates, // All candidates have 0 instances.
			wantErr: false,
		},
		{
			name: "spread/permissive: uneven distribution returns nodes with min instances",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Place instances: member01=2, member02=2, member03=1, member04=0, member05=0.
					nodeCounts := map[string]int{
						"member01": 2,
						"member02": 2,
						"member03": 1,
					}

					instNum := 1
					for node, count := range nodeCounts {
						for range count {
							instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
								Name:    fmt.Sprintf("c%d", instNum),
								Node:    node,
								Project: "default",
								Type:    instancetype.Container,
							})
							s.Require().NoError(err)

							err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
								"placement.group": "pg-spread-permissive",
							})
							s.Require().NoError(err)

							instNum++
						}
					}

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-spread-permissive",
					Description: "Spread permissive placement group",
				},
			},
			want:    candidatesOnly("member04", "member05"), // Only nodes with 0 instances (min).
			wantErr: false,
			caseTearDown: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					for i := 1; i <= 5; i++ {
						_ = cluster.DeleteInstance(ctx, tx.Tx(), "default", fmt.Sprintf("c%d", i))
					}

					return nil
				})
			},
		},

		// Policy: compact
		// Rigor: strict
		{
			name: "compact/strict: initial placement",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					pgID, err := cluster.CreatePlacementGroup(ctx, tx.Tx(), cluster.PlacementGroup{
						Project:     "default",
						Name:        "pg-compact-strict",
						Description: "Compact strict placement group",
					})
					s.Require().NoError(err)

					err = cluster.CreatePlacementGroupConfig(ctx, tx.Tx(), pgID, map[string]string{
						"policy": api.PlacementPolicyCompact,
						"rigor":  api.PlacementRigorStrict,
					})
					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-compact-strict",
					Description: "Compact strict placement group",
				},
			},
			want:    candidates, // No instances yet, all candidates valid.
			wantErr: false,
		},
		{
			name: "compact/strict: second instance must use same member",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Place first instance on member03.
					instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
						Name:    "c1",
						Node:    "member03",
						Project: "default",
						Type:    instancetype.Container,
					})
					s.Require().NoError(err)

					err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
						"placement.group": "pg-compact-strict",
					})
					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-compact-strict",
					Description: "Compact strict placement group",
				},
			},
			want:    candidatesOnly("member03"), // Only member03.
			wantErr: false,
		},
		{
			name: "compact/strict: target member unavailable should return error",
			args: args{
				candidates: candidatesWithout("member03"), // member03 not in candidates.
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-compact-strict",
					Description: "Compact strict placement group",
				},
			},
			want:    nil,
			wantErr: true, // Required member not available.
			caseTearDown: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_ = cluster.DeleteInstance(ctx, tx.Tx(), "default", "c1")
					return nil
				})
			},
		},

		// Policy: compact
		// Rigor: permissive
		{
			name: "compact/permissive: initial placement",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					pgID, err := cluster.CreatePlacementGroup(ctx, tx.Tx(), cluster.PlacementGroup{
						Project:     "default",
						Name:        "pg-compact-permissive",
						Description: "Compact permissive placement group",
					})
					s.Require().NoError(err)

					err = cluster.CreatePlacementGroupConfig(ctx, tx.Tx(), pgID, map[string]string{
						"policy": api.PlacementPolicyCompact,
						"rigor":  api.PlacementRigorPermissive,
					})
					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-compact-permissive",
					Description: "Compact permissive placement group",
				},
			},
			want:    candidates, // No instances yet, all candidates valid.
			wantErr: false,
		},
		{
			name: "compact/permissive: prefer same member when available",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Place first instance on member02.
					instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
						Name:    "c1",
						Node:    "member02",
						Project: "default",
						Type:    instancetype.Container,
					})
					s.Require().NoError(err)

					err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
						"placement.group": "pg-compact-permissive",
					})
					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-compact-permissive",
					Description: "Compact permissive placement group",
				},
			},
			want:    candidatesOnly("member02"), // Prefer member02.
			wantErr: false,
		},
		{
			name: "compact/permissive: fallback to all candidates when preferred unavailable",
			args: args{
				candidates: candidatesWithout("member02"), // member02 not in candidates.
				project:    "default",
				placementGroup: cluster.PlacementGroup{
					Project:     "default",
					Name:        "pg-compact-permissive",
					Description: "Compact permissive placement group",
				},
			},
			want:    candidatesWithout("member02"), // All other candidates.
			wantErr: false,
		},
	}

	// Prepare a placement group cache to avoid reloading the same group repeatedly.
	pgCache := NewCache()

	for i, tt := range tests {
		s.T().Logf("Case %d: %s", i, tt.name)
		if tt.caseSetup != nil {
			tt.caseSetup()
		}

		_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Fetch the placement group to get the full object with ID.
			placementGroup, err := pgCache.Get(ctx, tx, tt.args.placementGroup.Name, tt.args.placementGroup.Project)
			if err != nil {
				return err
			}

			apiPlacementGroup, err := placementGroup.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			got, err := Filter(ctx, tx, tt.args.candidates, *apiPlacementGroup, false)
			if tt.wantErr {
				s.Error(err)
				return nil
			}

			s.Require().NoError(err)
			s.ElementsMatch(tt.want, got)
			return nil
		})

		if tt.caseTearDown != nil {
			tt.caseTearDown()
		}
	}
}
