package placement

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

type filteringSuite struct {
	suite.Suite
}

func TestFilteringSuite(t *testing.T) {
	suite.Run(t, new(filteringSuite))
}

func (s *filteringSuite) TestApplyRuleset() {
	testCluster, cleanup := db.NewTestCluster(s.T())
	defer cleanup()

	nodeNames := []string{"member01", "member02", "member03", "member04", "member05"}
	candidates := make([]db.NodeInfo, 0, len(nodeNames))
	for i, nodeName := range nodeNames {
		candidates = append(candidates, db.NodeInfo{Name: nodeName, Address: fmt.Sprintf("192.0.2.%d", i)})
	}

	candidatesWithout := func(members ...string) []db.NodeInfo {
		filteredCandidates := make([]db.NodeInfo, 0, len(candidates))
		for _, candidate := range candidates {
			if !shared.ValueInSlice(candidate.Name, members) {
				filteredCandidates = append(filteredCandidates, candidate)
			}
		}

		return filteredCandidates
	}

	err := testCluster.Transaction(context.Background(), func(_ context.Context, tx *db.ClusterTx) error {
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
		expandedConfig map[string]string
		ruleset        cluster.PlacementRuleset
	}

	tests := []struct {
		name         string
		args         args
		caseSetup    func()
		caseTearDown func()
		want         []db.NodeInfo
		wantErr      error
	}{
		{
			name: "cluster group affinity",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := cluster.CreateClusterGroup(ctx, tx.Tx(), cluster.ClusterGroup{Name: "g1"})
					s.Require().NoError(err)

					for _, node := range candidatesWithout("member01", "member02") {
						err = tx.AddNodeToClusterGroup(ctx, "g1", node.Name)
						s.Require().NoError(err)
					}

					s.Require().NoError(err)

					return nil
				})
			},
			args: args{
				candidates:     candidates,
				project:        "default",
				expandedConfig: map[string]string{},
				ruleset: cluster.PlacementRuleset{
					Project:     "default",
					Name:        "cluster_group_affinity",
					Description: "Cluster group affinity (g1)",
					PlacementRules: []cluster.PlacementRule{
						{
							Required: true,
							Kind:     cluster.PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
							Priority: 0,
							Selector: cluster.Selector{
								EntityType: cluster.EntityType(entity.TypeClusterGroup),
								Matchers: []api.SelectorMatcher{
									{
										Property: "name",
										Values:   []string{"g1"},
									},
								},
							},
						},
					},
				},
			},
			want: candidatesWithout("member01", "member02"),
		},
		{
			name: "instance anti-affinity (first)",
			args: args{
				candidates: candidates,
				project:    "default",
				expandedConfig: map[string]string{
					"user.deployment": "ha",
				},
				ruleset: cluster.PlacementRuleset{
					Project:     "default",
					Name:        "instance_anti_affinity",
					Description: "Instance anti-affinity",
					PlacementRules: []cluster.PlacementRule{
						{
							Required: true,
							Kind:     cluster.PlacementRuleKind(api.PlacementRuleKindMemberAntiAffinity),
							Priority: 0,
							Selector: cluster.Selector{
								EntityType: cluster.EntityType(entity.TypeInstance),
								Matchers: []api.SelectorMatcher{
									{
										Property: "config.user.deployment",
										Values:   []string{"ha"},
									},
								},
							},
						},
					},
				},
			},
			want: candidates,
		},
		{
			name: "instance anti-affinity (second)",
			caseSetup: func() {
				_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
						Name:    "c1",
						Node:    "member01",
						Project: "default",
						Type:    instancetype.Container,
					})
					s.Require().NoError(err)
					err = cluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
						"user.deployment": "ha",
					})
					s.Require().NoError(err)
					return nil
				})
			},
			args: args{
				candidates: candidates,
				project:    "default",
				expandedConfig: map[string]string{
					"user.deployment": "ha",
				},
				ruleset: cluster.PlacementRuleset{
					Project:     "default",
					Name:        "instance_anti_affinity",
					Description: "Instance anti-affinity",
					PlacementRules: []cluster.PlacementRule{
						{
							Required: true,
							Kind:     cluster.PlacementRuleKind(api.PlacementRuleKindMemberAntiAffinity),
							Priority: 0,
							Selector: cluster.Selector{
								EntityType: cluster.EntityType(entity.TypeInstance),
								Matchers: []api.SelectorMatcher{
									{
										Property: "config.user.deployment",
										Values:   []string{"ha"},
									},
								},
							},
						},
					},
				},
			},
			want: candidatesWithout("member01"),
		},
	}

	for _, tt := range tests {
		if tt.caseSetup != nil {
			tt.caseSetup()
		}

		_ = testCluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			got, err := ApplyRuleset(ctx, tx.Tx(), tt.args.candidates, tt.args.expandedConfig, nil, tt.args.ruleset)
			if tt.wantErr != nil {
				s.Equal(tt.wantErr, err)
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
