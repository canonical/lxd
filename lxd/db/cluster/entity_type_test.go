package cluster

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/shared/api"
)

type selectorSuite struct {
	suite.Suite
	db *sql.DB
}

func TestSelectorSuite(t *testing.T) {
	suite.Run(t, new(selectorSuite))
}

func (s *selectorSuite) SetupTest() {
	s.db = newDB(s.T())
}

func (s *selectorSuite) Test_entityTypeClusterGroup_selectorQuery() {
	tests := []struct {
		name          string
		selector      Selector
		expectedQuery string
		expectedArgs  []any
		expectedErr   error
	}{
		{
			selector: Selector{
				ID:         0,
				EntityType: EntityType("cluster_group"),
				Matchers: SelectorMatchers{
					{
						Property: "name",
						Values:   []string{"g1", "g2"},
					},
				},
			},
			expectedQuery: `SELECT id FROM cluster_groups WHERE name IN (?, ?)`,
			expectedArgs:  []any{"g1", "g2"},
		},
	}

	for _, tt := range tests {
		query, args, err := entityTypeClusterGroup{}.selectorQuery(tt.selector)
		if tt.expectedErr != nil {
			s.Equal(tt.expectedErr, err)
			return
		}

		s.Require().NoError(err)

		_, err = s.db.Prepare(query)
		s.Require().NoError(err)

		s.Equal(tt.expectedQuery, query)
		s.ElementsMatch(tt.expectedArgs, args)
	}
}

func (s *selectorSuite) Test_entityTypeInstance_propertyQuery() {
	tests := []struct {
		name                      string
		selector                  Selector
		expectedHasConfigProperty bool
		expectedQuery             string
		expectedArgs              []any
		expectedErr               error
	}{
		{
			selector: Selector{
				ID:         0,
				EntityType: EntityType("instance"),
				Matchers: SelectorMatchers{
					{
						Property: "project",
						Values:   []string{"p1"},
					},
					{
						Property: "config.user.deployment",
						Values:   []string{"ha1", "ha2"},
					},
				},
			},
			expectedHasConfigProperty: true,
			expectedQuery:             `SELECT instances.id FROM instances JOIN projects ON instances.project_id = projects.id WHERE projects.name IN (?)`,
			expectedArgs:              []any{"p1"},
		},
	}

	for _, tt := range tests {
		query, args, hasConfigProperty, err := entityTypeInstance{}.propertyQuery(tt.selector)
		if tt.expectedErr != nil {
			s.Equal(tt.expectedErr, err)
			return
		}

		s.Require().NoError(err)

		_, err = s.db.Prepare(query)
		s.Require().NoError(err)

		s.Equal(tt.expectedHasConfigProperty, hasConfigProperty)
		s.Equal(tt.expectedQuery, query)
		s.ElementsMatch(tt.expectedArgs, args)
	}
}

func (s *selectorSuite) Test_entityTypeInstance_configQuery() {
	tests := []struct {
		name            string
		selector        Selector
		instanceIDs     []int
		expectedMatcher api.SelectorMatcher
		expectedQuery   string
		expectedArgs    []any
		expectedErr     error
	}{
		{
			selector: Selector{
				ID:         0,
				EntityType: EntityType("instance"),
				Matchers: SelectorMatchers{
					{
						Property: "project",
						Values:   []string{"p1"},
					},
					{
						Property: "config.user.deployment",
						Values:   []string{"ha1", "ha2"},
					},
				},
			},
			instanceIDs: []int{1, 2, 3},
			expectedQuery: `
SELECT 
	instances.id AS instance_id, 
	1000000 AS apply_order, 
	instances_config.value 
FROM instances 
JOIN instances_config ON instances.id = instances_config.instance_id 
WHERE instances_config.key = ? 
	AND instances.id IN (?, ?, ?)
UNION 
SELECT 
	instances.id AS instance_id, 
	instances_profiles.apply_order, 
	profiles_config.value 
FROM instances 
JOIN instances_profiles ON instances.id = instances_profiles.instance_id 
JOIN profiles ON instances_profiles.profile_id = profiles.id 
JOIN profiles_config ON profiles.id = profiles_config.profile_id 
WHERE profiles_config.key = ? 
	AND instances.id IN (?, ?, ?)`,
			expectedArgs: []any{"user.deployment", 1, 2, 3, "user.deployment", 1, 2, 3},
			expectedMatcher: api.SelectorMatcher{
				Property: "config.user.deployment",
				Values:   []string{"ha1", "ha2"},
			},
		},
	}

	for _, tt := range tests {
		query, matcher, args, err := entityTypeInstance{}.configQuery(tt.selector, tt.instanceIDs)
		if tt.expectedErr != nil {
			s.Equal(tt.expectedErr, err)
			return
		}

		s.Require().NoError(err)

		_, err = s.db.Prepare(query)
		s.Require().NoError(err)

		s.Equal(tt.expectedMatcher, matcher)
		s.Equal(tt.expectedQuery, query)
		s.ElementsMatch(tt.expectedArgs, args)
	}
}
