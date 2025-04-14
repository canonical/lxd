package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

type placementSuite struct {
	suite.Suite
}

func TestPlacementSuite(t *testing.T) {
	suite.Run(t, new(placementSuite))
}

func (s *placementSuite) TestSortedRules() {
	tests := []struct {
		name    string
		ruleset PlacementRuleset
		want    []PlacementRule
	}{
		{
			name: "Valid rules",
			ruleset: PlacementRuleset{
				Name:        "my-ruleset",
				Project:     "my-project",
				Description: "test ruleset",
				PlacementRules: []PlacementRule{
					{
						Required: true,
						Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
						Selector: Selector{
							EntityType: EntityType(entity.TypeClusterGroup),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					{
						Required: false,
						Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
						Priority: 0,
						Selector: Selector{
							EntityType: EntityType(entity.TypeClusterGroup),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					{
						Required: false,
						Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
						Priority: 1,
						Selector: Selector{
							EntityType: EntityType(entity.TypeClusterGroup),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					{
						Required: true,
						Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
						Selector: Selector{
							EntityType: EntityType(entity.TypeInstance),
							Matchers: []api.SelectorMatcher{
								{
									Property: "name",
									Values:   []string{"g1"},
								},
							},
						},
					},
					{
						Required: false,
						Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
						Priority: 1,
						Selector: Selector{
							EntityType: EntityType(entity.TypeInstance),
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
			want: []PlacementRule{
				{
					Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
					Required: true,
					Selector: Selector{
						EntityType: EntityType("cluster_group"),
						Matchers: SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
						},
					},
				},
				{
					Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
					Required: true,
					Selector: Selector{
						EntityType: EntityType("instance"),
						Matchers: SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
							{
								Property: "project",
								Values:   []string{"my-project"},
							},
						},
					},
				},
				{
					Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
					Priority: 1,
					Selector: Selector{
						EntityType: EntityType("cluster_group"),
						Matchers: SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
						},
					},
				},
				{
					Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
					Priority: 1,
					Selector: Selector{
						EntityType: EntityType("instance"),
						Matchers: SelectorMatchers{
							{
								Property: "name",
								Values:   []string{"g1"},
							},
							{
								Property: "project",
								Values:   []string{"my-project"},
							},
						},
					},
				},
				{
					Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
					Priority: 0,
					Selector: Selector{
						EntityType: EntityType("cluster_group"),
						Matchers: SelectorMatchers{
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
		got := tt.ruleset.SortedRules()

		s.Require().Len(got, len(tt.want))
		for i := range tt.want {
			s.Require().Equal(tt.want[i], got[i])
		}
	}
}

func (s *placementSuite) TestGetRules() {
	db := newDB(s.T())
	stmts, err := PrepareStmts(db, false)
	s.Require().NoError(err)
	PreparedStmts = stmts

	tx, err := db.Begin()
	s.Require().NoError(err)
	_, err = CreateProject(context.Background(), tx, Project{Name: "default"})
	s.Require().NoError(err)

	projectName := "default"
	rulesetName := "test-ruleset"
	expectedRuleset := PlacementRuleset{
		ID:          1,
		Project:     projectName,
		Name:        rulesetName,
		Description: "Test ruleset",
	}

	_, err = CreatePlacementRuleset(context.Background(), tx, expectedRuleset)
	s.Require().NoError(err)

	rulesets, err := GetPlacementRulesets(context.Background(), tx, nil)
	s.Require().NoError(err)
	s.Require().Len(rulesets, 1)
	s.Require().Equal(expectedRuleset, rulesets[0])

	rulesets, err = GetPlacementRulesets(context.Background(), tx, &PlacementRulesetFilter{Project: &projectName})
	s.Require().NoError(err)
	s.Require().Len(rulesets, 1)
	s.Require().Equal(expectedRuleset, rulesets[0])

	rulesets, err = GetPlacementRulesets(context.Background(), tx, &PlacementRulesetFilter{Project: &projectName, Name: &rulesetName})
	s.Require().NoError(err)
	s.Require().Len(rulesets, 1)
	s.Require().Equal(expectedRuleset, rulesets[0])

	gotRuleset, err := GetPlacementRuleset(context.Background(), tx, projectName, rulesetName)
	s.Require().NoError(err)
	s.Require().Equal(expectedRuleset, *gotRuleset)

	expectedRuleset.PlacementRules = []PlacementRule{
		{
			ID:       1,
			Name:     "test-rule-name",
			Required: false,
			Priority: 1,
			Kind:     PlacementRuleKind(api.PlacementRuleKindMemberAffinity),
			Selector: Selector{
				ID:         1,
				EntityType: EntityType(entity.TypeClusterGroup),
				Matchers: SelectorMatchers{
					{
						Property: "name",
						Values:   []string{"group1"},
					},
				},
			},
		},
	}

	err = UpdatePlacementRuleset(context.Background(), tx, projectName, rulesetName, expectedRuleset)
	s.Require().NoError(err)

	rulesets, err = GetPlacementRulesets(context.Background(), tx, nil)
	s.Require().NoError(err)
	s.Require().Len(rulesets, 1)
	s.Require().Equal(expectedRuleset, rulesets[0])

	rulesets, err = GetPlacementRulesets(context.Background(), tx, &PlacementRulesetFilter{Project: &projectName})
	s.Require().NoError(err)
	s.Require().Len(rulesets, 1)
	s.Require().Equal(expectedRuleset, rulesets[0])

	rulesets, err = GetPlacementRulesets(context.Background(), tx, &PlacementRulesetFilter{Project: &projectName, Name: &rulesetName})
	s.Require().NoError(err)
	s.Require().Len(rulesets, 1)
	s.Require().Equal(expectedRuleset, rulesets[0])

	gotRuleset, err = GetPlacementRuleset(context.Background(), tx, projectName, rulesetName)
	s.Require().NoError(err)
	s.Require().Equal(expectedRuleset, *gotRuleset)

	err = DeletePlacementRuleset(context.Background(), tx, projectName, rulesetName)
	s.Require().NoError(err)

	err = tx.Commit()
	s.Require().NoError(err)
}
