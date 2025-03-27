package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
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
