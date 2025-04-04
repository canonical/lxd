package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

type placementSuite struct {
	suite.Suite
}

func TestPlacementSuite(t *testing.T) {
	suite.Run(t, new(placementSuite))
}

func (s *placementSuite) Test_getSortedPlacementRules() {
	type args struct {
		rules map[string]api.InstancePlacementRule
	}

	tests := []struct {
		name    string
		args    args
		want    []dbCluster.InstancePlacementRule
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "Valid rules",
			args: args{
				rules: map[string]api.InstancePlacementRule{
					"2": {
						Required: true,
						Kind:     api.InstancePlacementRuleKindAffinity,
						Selector: api.Selector{
							EntityType: string(entity.TypeClusterGroup),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					"1": {
						Required: false,
						Kind:     api.InstancePlacementRuleKindAffinity,
						Priority: 0,
						Selector: api.Selector{
							EntityType: string(entity.TypeClusterGroup),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					"3": {
						Required: false,
						Kind:     api.InstancePlacementRuleKindAffinity,
						Priority: 1,
						Selector: api.Selector{
							EntityType: string(entity.TypeClusterGroup),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					"4": {
						Required: true,
						Kind:     api.InstancePlacementRuleKindAffinity,
						Selector: api.Selector{
							EntityType: string(entity.TypeInstance),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					"5": {
						Required: false,
						Kind:     api.InstancePlacementRuleKindAffinity,
						Priority: 1,
						Selector: api.Selector{
							EntityType: string(entity.TypeInstance),
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
			want: []dbCluster.InstancePlacementRule{
				{
					Name:     "2",
					Kind:     dbCluster.InstancePlacementRuleKind(api.InstancePlacementRuleKindAffinity),
					Required: true,
					Selector: dbCluster.Selector{
						EntityType: dbCluster.EntityType("cluster_group"),
						Matchers: dbCluster.SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
						},
					},
				},
				{
					Name:     "4",
					Kind:     dbCluster.InstancePlacementRuleKind(api.InstancePlacementRuleKindAffinity),
					Required: true,
					Selector: dbCluster.Selector{
						EntityType: dbCluster.EntityType("instance"),
						Matchers: dbCluster.SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
						},
					},
				},
				{
					Name:     "3",
					Kind:     dbCluster.InstancePlacementRuleKind(api.InstancePlacementRuleKindAffinity),
					Priority: 1,
					Selector: dbCluster.Selector{
						EntityType: dbCluster.EntityType("cluster_group"),
						Matchers: dbCluster.SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
						},
					},
				},
				{
					Name:     "5",
					Kind:     dbCluster.InstancePlacementRuleKind(api.InstancePlacementRuleKindAffinity),
					Priority: 1,
					Selector: dbCluster.Selector{
						EntityType: dbCluster.EntityType("instance"),
						Matchers: dbCluster.SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
						},
					},
				},
				{
					Name:     "1",
					Kind:     dbCluster.InstancePlacementRuleKind(api.InstancePlacementRuleKindAffinity),
					Priority: 0,
					Selector: dbCluster.Selector{
						EntityType: dbCluster.EntityType("cluster_group"),
						Matchers: dbCluster.SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
						},
					},
				},
			},
		},
	}

	for i, tt := range tests {
		s.T().Logf("%s (case %d)", tt.name, i)
		got, err := getSortedPlacementRules(tt.args.rules)
		if tt.wantErr != nil {
			tt.wantErr(s.T(), err)
		}

		s.Require().Len(got, len(tt.want))
		for i := range tt.want {
			s.Require().Equal(tt.want[i], got[i])
		}
	}
}

func (s *placementSuite) Test_applyPlacementRules() {
	cluster, cleanup := db.NewTestCluster(s.T())
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

	err := cluster.Transaction(context.Background(), func(_ context.Context, tx *db.ClusterTx) error {
		for i, node := range candidates {
			id, err := tx.CreateNode(node.Name, node.Address)
			candidates[i].ID = id
			s.Require().NoError(err)
		}

		return nil
	})
	s.Require().NoError(err)

	type args struct {
		candidates             []db.NodeInfo
		project                string
		expandedConfig         map[string]string
		expandedPlacementRules map[string]api.InstancePlacementRule
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
				_ = cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					_, err := dbCluster.CreateClusterGroup(ctx, tx.Tx(), dbCluster.ClusterGroup{Name: "g1"})
					s.Require().NoError(err)

					for _, node := range candidatesWithout("member01", "member02") {
						err = tx.AddNodeToClusterGroup(ctx, "g1", node.Name)
						s.Require().NoError(err)
					}

					return nil
				})
			},
			args: args{
				candidates:     candidates,
				project:        "default",
				expandedConfig: map[string]string{},
				expandedPlacementRules: map[string]api.InstancePlacementRule{
					"cluster_group_affinity": {
						Required: true,
						Kind:     api.InstancePlacementRuleKindAffinity,
						Priority: 0,
						Selector: api.Selector{
							EntityType: "cluster_group",
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
				expandedPlacementRules: map[string]api.InstancePlacementRule{
					"instance_anti_affinity": {
						Required: true,
						Kind:     api.InstancePlacementRuleKindAntiAffinity,
						Priority: 0,
						Selector: api.Selector{
							EntityType: "instance",
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
			want: candidates,
		},
		{
			name: "instance anti-affinity (second)",
			caseSetup: func() {
				_ = cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					instanceID, err := dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{
						Name:    "c1",
						Node:    "member01",
						Project: "default",
						Type:    instancetype.Container,
					})
					s.Require().NoError(err)
					err = dbCluster.CreateInstanceConfig(ctx, tx.Tx(), instanceID, map[string]string{
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
				expandedPlacementRules: map[string]api.InstancePlacementRule{
					"instance_anti_affinity": {
						Required: true,
						Kind:     api.InstancePlacementRuleKindAntiAffinity,
						Priority: 0,
						Selector: api.Selector{
							EntityType: "instance",
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
			want: candidatesWithout("member01"),
		},
	}

	for _, tt := range tests {
		if tt.caseSetup != nil {
			tt.caseSetup()
		}

		_ = cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			got, err := applyPlacementRules(ctx, tx.Tx(), tt.args.candidates, tt.args.project, tt.args.expandedConfig, tt.args.expandedPlacementRules)
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
