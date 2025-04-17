package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	dqliteDriver "github.com/canonical/go-dqlite/v3/driver"
	"github.com/mattn/go-sqlite3"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/validate"
)

// PlacementRuleKind is the database enum for the api.PlacementRuleKind constants.
type PlacementRuleKind api.PlacementRuleKind

const (
	placementRuleKindCodeMemberAffinity     int64 = 1
	placementRuleKindCodeMemberAntiAffinity int64 = 2
)

// Scan implements sql.Scanner for InstancePlacementRuleKind. This converts the integer value back into the correct API constant or
// returns an error.
func (a *PlacementRuleKind) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("Instance placement rule kind cannot be null")
	}

	intValue, err := driver.Int32.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid instance placement rule kind type: %w", err)
	}

	ruleInt, ok := intValue.(int64)
	if !ok {
		return fmt.Errorf("Instance placement rule kind should be an integer, got `%v` (%T)", intValue, intValue)
	}

	switch ruleInt {
	case placementRuleKindCodeMemberAffinity:
		*a = PlacementRuleKind(api.PlacementRuleKindMemberAffinity)
	case placementRuleKindCodeMemberAntiAffinity:
		*a = PlacementRuleKind(api.PlacementRuleKindMemberAntiAffinity)
	default:
		return fmt.Errorf("Unknown instance placement rule kind `%d`", ruleInt)
	}

	return nil
}

// Value implements driver.Valuer for InstancePlacementRuleKind. This converts the API constant into an integer or throws an error.
func (a PlacementRuleKind) Value() (driver.Value, error) {
	switch api.PlacementRuleKind(a) {
	case api.PlacementRuleKindMemberAffinity:
		return placementRuleKindCodeMemberAffinity, nil
	case api.PlacementRuleKindMemberAntiAffinity:
		return placementRuleKindCodeMemberAntiAffinity, nil
	}

	return nil, fmt.Errorf("Invalid instance placement rule kind %q", a)
}

// PlacementRule is the database representation of an api.PlacementRule.
type PlacementRule struct {
	ID       int
	Name     string
	Kind     PlacementRuleKind
	Priority int
	Required bool
	Selector Selector
}

// ToAPI converts an PlacementRule into an api.PlacementRule.
func (i PlacementRule) ToAPI() (string, api.PlacementRule) {
	return i.Name, api.PlacementRule{
		Required: i.Required,
		Kind:     api.PlacementRuleKind(i.Kind),
		Priority: i.Priority,
		Selector: api.Selector{
			EntityType: string(i.Selector.EntityType),
			Matchers:   i.Selector.Matchers,
		},
	}
}

// PlacementRulesToAPI converts a slice of PlacementRule to a slice of api.PlacementRule.
func PlacementRulesToAPI(rules []PlacementRule) map[string]api.PlacementRule {
	apiRules := make(map[string]api.PlacementRule, len(rules))
	for _, rule := range rules {
		name, apiRule := rule.ToAPI()
		apiRules[name] = apiRule
	}

	return apiRules
}

// PlacementRuleFromAPI parses and validates the given rulename and api.PlacementRule, returning an
// PlacementRule or an error.
func PlacementRuleFromAPI(name string, rule api.PlacementRule, projectIsRestricted bool, allowedClusterGroups []string) (*PlacementRule, error) {
	err := validate.IsDeviceName(name)
	if err != nil {
		return nil, err
	}

	if rule.Priority < 0 || rule.Priority > 255 {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Instance placement rule priority must be in the range 0-255")
	}

	kind := PlacementRuleKind(rule.Kind)
	_, err = kind.Value()
	if err != nil {
		return nil, err
	}

	selectorEntityType := entity.Type(rule.Selector.EntityType)
	if !shared.ValueInSlice(selectorEntityType, []entity.Type{entity.TypeInstance, entity.TypeClusterGroup}) {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Instance placement rule selector entity type must be one of %q, or %q", entity.TypeInstance, entity.TypeClusterGroup)
	}

	existingProperties := make([]string, 0, len(rule.Selector.Matchers))
	for _, matcher := range rule.Selector.Matchers {
		if len(matcher.Values) == 0 {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Selector matcher on entity type %q with property %q missing values", selectorEntityType, matcher.Property)
		}

		if shared.ValueInSlice(matcher.Property, existingProperties) {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Duplicate selector matcher property %q found in instance placement rule selector for entity type %q", matcher.Property, rule.Selector.EntityType)
		}

		switch selectorEntityType {
		case entity.TypeInstance:
			if matcher.Property != "name" && !strings.HasPrefix(matcher.Property, "config.") {
				return nil, api.StatusErrorf(http.StatusBadRequest, "Matcher property %q not allowed for entity type %q", matcher.Property, entity.TypeInstance)
			}

		case entity.TypeClusterGroup:
			if matcher.Property != "name" {
				return nil, api.StatusErrorf(http.StatusBadRequest, "Matcher property %q not allowed for entity type %q", matcher.Property, entity.TypeClusterGroup)
			}

			if projectIsRestricted {
				for _, value := range matcher.Values {
					if !shared.ValueInSlice(value, allowedClusterGroups) {
						return nil, api.StatusErrorf(http.StatusForbidden, "Ruleset contains disallowed cluster group %q", value)
					}
				}
			}
		}

		existingProperties = append(existingProperties, matcher.Property)
	}

	i := &PlacementRule{
		Name:     name,
		Kind:     kind,
		Priority: rule.Priority,
		Required: rule.Required,
		Selector: Selector{
			EntityType: EntityType(selectorEntityType),
			Matchers:   rule.Selector.Matchers,
		},
	}

	return i, nil
}

// Selector is the database representation of an api.Selector.
type Selector struct {
	ID         int
	EntityType EntityType
	Matchers   SelectorMatchers
}

// SelectorMatchers is a database representation of an array of api.SelectorMatcher.
// This is used to automatically write selector matchers as JSON to the database.
type SelectorMatchers []api.SelectorMatcher

// Scan implements sql.Scanner. This unmarshals the matchers as JSON.
func (v *SelectorMatchers) Scan(value any) error {
	if value == nil {
		*v = []api.SelectorMatcher{}
		return nil
	}

	strValue, err := driver.String.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid selector matcher type: %w", err)
	}

	valueStr, ok := strValue.(string)
	if !ok {
		return fmt.Errorf("Selector matcher should be a string, got `%v` (%T)", strValue, strValue)
	}

	if valueStr == "" {
		*v = []api.SelectorMatcher{}
		return nil
	}

	return json.Unmarshal([]byte(valueStr), v)
}

// Value implements driver.Valuer for SelectorMatchers. This returns the matchers as marshalled JSON.
func (v SelectorMatchers) Value() (driver.Value, error) {
	if len(v) == 0 {
		return "", nil
	}

	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return string(b), nil
}

// PlacementRuleset is the database representation of an api.PlacementRuleset.
type PlacementRuleset struct {
	ID             int
	Project        string
	Name           string
	Description    string
	PlacementRules []PlacementRule
}

// PlacementRulesetFromAPI parses and validates the given map of api.PlacementRule, returning a slice of
// PlacementRule or an error.
func PlacementRulesetFromAPI(ruleset api.PlacementRuleset, projectIsRestricted bool, allowedClusterGroups []string) (*PlacementRuleset, error) {
	err := validate.IsDeviceName(ruleset.Name)
	if err != nil {
		return nil, err
	}

	dbRules := make([]PlacementRule, 0, len(ruleset.PlacementRules))
	priorities := make(map[int]struct{})
	for name, rule := range ruleset.PlacementRules {
		i, err := PlacementRuleFromAPI(name, rule, projectIsRestricted, allowedClusterGroups)
		if err != nil {
			return nil, err
		}

		if !i.Required {
			_, exists := priorities[i.Priority]
			if exists {
				return nil, api.StatusErrorf(http.StatusBadRequest, "Placement ruleset %q contains two or more optional rules with equal priority", ruleset.Name)
			}
		}

		dbRules = append(dbRules, *i)
	}

	return &PlacementRuleset{
		Project:        ruleset.Project,
		Name:           ruleset.Name,
		Description:    ruleset.Description,
		PlacementRules: dbRules,
	}, nil
}

// ToAPI converts the PlacementRuleset to an api.PlacementRuleset.
func (p PlacementRuleset) ToAPI() api.PlacementRuleset {
	return api.PlacementRuleset{
		Name:           p.Name,
		Project:        p.Project,
		Description:    p.Description,
		PlacementRules: PlacementRulesToAPI(p.PlacementRules),
	}
}

// SortedRules returns a slice of PlacementRule, sorted by their order of application.
func (p PlacementRuleset) SortedRules() []PlacementRule {
	rules := slices.Clone(p.PlacementRules)

	// Implicitly add the project matcher for instances.
	for i, rule := range rules {
		if entity.Type(rule.Selector.EntityType) == entity.TypeInstance {
			rule.Selector.Matchers = append(rule.Selector.Matchers, api.SelectorMatcher{
				Property: "project",
				Values:   []string{p.Project},
			})

			rules[i] = rule
		}
	}

	slices.SortFunc(rules, func(a, b PlacementRule) int {
		if a.Required && b.Required {
			return 0
		}

		if a.Required {
			return -1
		}

		if b.Required {
			// b > a
			return 1
		}

		return b.Priority - a.Priority
	})

	return rules
}

// CreatePlacementRuleset creates a new PlacementRuleset and associated rules.
func CreatePlacementRuleset(ctx context.Context, tx *sql.Tx, ruleset PlacementRuleset) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO placement_rulesets (name, description, project_id) VALUES (?, ?, (SELECT id FROM projects WHERE name = ?))`, ruleset.Name, ruleset.Description, ruleset.Project)
	if err != nil {
		var driverErr dqliteDriver.Error
		if errors.As(err, &driverErr) && driverErr.Code == int(sqlite3.ErrConstraintUnique) {
			return -1, api.StatusErrorf(http.StatusConflict, "A placement ruleset with name %q already exists", ruleset.Name)
		}

		return -1, fmt.Errorf("Failed to create placement ruleset: %w", err)
	}

	rulesetID, err := res.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed to retrieve ruleset ID: %w", err)
	}

	err = UpsertPlacementRules(ctx, tx, int(rulesetID), ruleset.PlacementRules)
	if err != nil {
		return -1, err
	}

	return rulesetID, nil
}

// UpdatePlacementRuleset updates an existing PlacementRuleset and replaces it's rules.
func UpdatePlacementRuleset(ctx context.Context, tx *sql.Tx, projectName string, rulesetName string, ruleset PlacementRuleset) error {
	id, err := GetPlacementRulesetID(ctx, tx, projectName, rulesetName)
	if err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx, `UPDATE placement_rulesets SET description = ? WHERE id = ?`, ruleset.Description, id)
	if err != nil {
		return fmt.Errorf("Failed to update placement ruleset: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to validate placement ruleset update: %w", err)
	}

	if rowsAffected == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Placement ruleset %q not found", ruleset.Name)
	}

	err = UpsertPlacementRules(ctx, tx, id, ruleset.PlacementRules)
	if err != nil {
		return err
	}

	return nil
}

// RenamePlacementRuleset renames an existing PlacementRuleset.
func RenamePlacementRuleset(ctx context.Context, tx *sql.Tx, projectName string, oldName string, newName string) error {
	res, err := tx.ExecContext(ctx, `UPDATE placement_rulesets SET name = ? WHERE name = ? AND project_id = (SELECT id FROM projects WHERE name = ?)`, newName, oldName, projectName)
	if err != nil {
		return fmt.Errorf("Failed to update placement ruleset: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to validate placement ruleset update: %w", err)
	}

	if rowsAffected == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Placement ruleset %q not found", oldName)
	}

	return nil
}

// UpsertPlacementRules deletes any existing rules for the given ruleset and writes the given set of rules.
func UpsertPlacementRules(ctx context.Context, tx *sql.Tx, rulesetID int, rules []PlacementRule) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM placement_rules WHERE ruleset_id = ?", rulesetID)
	if err != nil {
		return fmt.Errorf("Failed to delete existing rules from placement ruleset: %w", err)
	}

	for _, rule := range rules {
		ruleID, err := query.UpsertObject(tx, "placement_rules", []string{"name", "kind", "required", "priority", "ruleset_id"}, []any{rule.Name, rule.Kind, rule.Required, rule.Priority, rulesetID})
		if err != nil {
			return fmt.Errorf("Failed to upsert placement rule: %w", err)
		}

		_, err = query.UpsertObject(tx, "placement_rules_selectors", []string{"entity_type", "matchers", "placement_rule_id"}, []any{rule.Selector.EntityType, rule.Selector.Matchers, ruleID})
		if err != nil {
			return fmt.Errorf("Failed to upsert placement rule selector: %w", err)
		}
	}

	return nil
}

// DeletePlacementRuleset deletes a single PlacementRuleset by name and project.
func DeletePlacementRuleset(ctx context.Context, tx *sql.Tx, projectName string, rulesetName string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM placement_rulesets WHERE name = ? AND project_id = (SELECT id FROM projects WHERE name = ?)`, rulesetName, projectName)
	if err != nil {
		return fmt.Errorf("Failed to deleted placement ruleset %q: %w", rulesetName, err)
	}

	nRows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to verify placement ruleset deletion: %w", err)
	}

	if nRows == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Ruleset %q not found", rulesetName)
	}

	if nRows > 1 {
		return fmt.Errorf("Multiple rulesets found with name %q in project %q", rulesetName, projectName)
	}

	return nil
}

// PlacementRulesetFilter contains fields that can be used to filter results when getting placement rulesets.
type PlacementRulesetFilter struct {
	Project *string
	Name    *string
}

// GetPlacementRulesets gets a slice of PlacementRuleset, filtered by the PlacementRulesetFilter.
func GetPlacementRulesets(ctx context.Context, tx *sql.Tx, filter *PlacementRulesetFilter) ([]PlacementRuleset, error) {
	var b strings.Builder
	b.WriteString(`
SELECT
	placement_rulesets.id,
	placement_rulesets.name,
	placement_rulesets.description,
	projects.name
FROM placement_rulesets
	JOIN projects ON projects.id = placement_rulesets.project_id
`)
	args := make([]any, 0, 2)
	if filter != nil {
		if filter.Name != nil && filter.Project == nil {
			return nil, fmt.Errorf("Cannot filter placement rulesets by name only, project must be provided")
		}

		if filter.Project != nil {
			b.WriteString(` WHERE projects.name = ?`)
			args = append(args, *filter.Project)
		}

		if filter.Name != nil {
			b.WriteString(` AND placement_rulesets.name = ?`)
			args = append(args, *filter.Name)
		}
	}

	rulesets := make(map[int]PlacementRuleset)
	err := query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		ruleset := PlacementRuleset{}
		err := scan(&ruleset.ID, &ruleset.Name, &ruleset.Description, &ruleset.Project)
		if err != nil {
			return err
		}

		rulesets[ruleset.ID] = ruleset
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	if len(rulesets) == 0 {
		return []PlacementRuleset{}, nil
	}

	args = make([]any, 0, len(rulesets))
	for id := range rulesets {
		args = append(args, id)
	}

	b.Reset()
	b.WriteString(`
SELECT
	placement_rules.ruleset_id,
	placement_rules.id,
	placement_rules.name,
	placement_rules.kind,
	placement_rules.required,
	placement_rules.priority,
	placement_rules_selectors.id,
	placement_rules_selectors.entity_type,
	placement_rules_selectors.matchers
FROM placement_rules
	JOIN placement_rules_selectors ON placement_rules_selectors.placement_rule_id = placement_rules.id
WHERE placement_rules.ruleset_id IN ` + query.Params(len(args)))
	err = query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		rule := PlacementRule{}
		var rulesetID int
		err := scan(&rulesetID, &rule.ID, &rule.Name, &rule.Kind, &rule.Required, &rule.Priority, &rule.Selector.ID, &rule.Selector.EntityType, &rule.Selector.Matchers)
		if err != nil {
			return err
		}

		ruleset := rulesets[rulesetID]
		ruleset.PlacementRules = append(ruleset.PlacementRules, rule)
		rulesets[rulesetID] = ruleset
		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to query placement rulesets: %w", err)
	}

	result := make([]PlacementRuleset, 0, len(rulesets))
	for _, ruleset := range rulesets {
		result = append(result, ruleset)
	}

	return result, nil
}

// GetPlacementRuleset gets a single PlacementRuleset by name and project.
func GetPlacementRuleset(ctx context.Context, tx *sql.Tx, project string, name string) (*PlacementRuleset, error) {
	rulesets, err := GetPlacementRulesets(ctx, tx, &PlacementRulesetFilter{
		Project: &project,
		Name:    &name,
	})
	if err != nil {
		return nil, err
	}

	if len(rulesets) == 0 {
		return nil, api.StatusErrorf(http.StatusNotFound, "No ruleset found with name %q in project %q", name, project)
	}

	if len(rulesets) > 1 {
		return nil, fmt.Errorf("Multiple rulesets found with name %q in project %q", name, project)
	}

	return &rulesets[0], nil
}

// GetPlacementRulesetID gets the ID of a ruleset by its project and name.
func GetPlacementRulesetID(ctx context.Context, tx *sql.Tx, projectName string, rulesetName string) (int, error) {
	q := `
SELECT placement_rulesets.id FROM placement_rulesets 
JOIN projects ON projects.id = placement_rulesets.project_id 
WHERE projects.name = ? AND placement_rulesets.name = ?
`

	var id int
	err := tx.QueryRowContext(ctx, q, projectName, rulesetName).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, api.StatusErrorf(http.StatusNotFound, "Placement ruleset %q not found", rulesetName)
		}

		return -1, fmt.Errorf("Failed to get placement ruleset ID: %w", err)
	}

	return id, nil
}

// GetPlacementRulesetNames returns a map of project name to slice of ruleset names. If a project name is provided, only
// rules in that project are returned. Otherwise, the returned map will contain all projects.
func GetPlacementRulesetNames(ctx context.Context, tx *sql.Tx, project *string) (map[string][]string, error) {
	var b strings.Builder
	b.WriteString(`
SELECT
	projects.name,
	placement_rulesets.name
FROM placement_rulesets
JOIN projects ON projects.id = placement_rulesets.project_id
`)

	var args []any
	if project != nil {
		b.WriteString(`WHERE projects.name = ?`)
		args = []any{*project}
	}

	nameMap := make(map[string][]string)
	err := query.Scan(ctx, tx, b.String(), func(scan func(dest ...any) error) error {
		var projectName string
		var rulesetName string
		err := scan(&projectName, &rulesetName)
		if err != nil {
			return err
		}

		nameMap[projectName] = append(nameMap[projectName], rulesetName)
		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to query placement ruleset names: %w", err)
	}

	return nameMap, nil
}

// GetPlacementRulesetUsedBy returns a list of URLs of all instances and profiles that reference the given ruleset in their configuration.
func GetPlacementRulesetUsedBy(ctx context.Context, tx *sql.Tx, projectName string, rulesetName string) ([]string, error) {
	q := `SELECT ` + strconv.Itoa(int(entityTypeCodeInstance)) + `, instances.name FROM instances
JOIN instances_config ON instances.id = instances_config.instance_id
JOIN projects ON instances.project_id = projects.id
WHERE instances_config.key = 'placement.ruleset' AND instances_config.value = ? AND projects.name = ?
UNION SELECT ` + strconv.Itoa(int(entityTypeCodeProfile)) + `, profiles.name FROM profiles
JOIN profiles_config ON profiles.id = profiles_config.profile_id
JOIN projects ON profiles.project_id = projects.id
WHERE profiles_config.key = 'placement.ruleset' AND profiles_config.value = ? AND projects.name = ?
`

	var urls []string
	err := query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var eType EntityType
		var eName string
		err := scan(&eType, &eName)
		if err != nil {
			return err
		}

		switch entity.Type(eType) {
		case entity.TypeInstance:
			urls = append(urls, api.NewURL().Project(projectName).Path("1.0", "instances", eName).String())
		case entity.TypeProfile:
			urls = append(urls, api.NewURL().Project(projectName).Path("1.0", "instances", eName).String())
		}

		return nil
	}, rulesetName, projectName, rulesetName, projectName)
	if err != nil {
		return nil, fmt.Errorf("Failed to find references to placement ruleset %q: %w", rulesetName, err)
	}

	return urls, nil
}

// GetAllPlacementRulesetUsedByURLs returns a map of project name to map of ruleset name to a list of URLs of instances and
// profiles that reference the rulesets in their configuration.
func GetAllPlacementRulesetUsedByURLs(ctx context.Context, tx *sql.Tx) (map[string]map[string][]string, error) {
	q := `SELECT ` + strconv.Itoa(int(entityTypeCodeInstance)) + `, instances.name, projects.name, instances_config.value FROM instances
JOIN instances_config ON instances.id = instances_config.instance_id
JOIN projects ON projects.id = instances.project_id
WHERE instances_config.key = 'placement.ruleset'
UNION SELECT ` + strconv.Itoa(int(entityTypeCodeProfile)) + `, profiles.name, projects.name, profiles_config.value FROM profiles
JOIN profiles_config ON profiles.id = profiles_config.profile_id
JOIN projects ON projects.id = profiles.project_id
WHERE profiles_config.key = 'placement.ruleset'
`

	urlMap := make(map[string]map[string][]string)
	err := query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var eType EntityType
		var eName string
		var projectName string
		var rulesetName string
		err := scan(&eType, &eName, &projectName, &rulesetName)
		if err != nil {
			return err
		}

		var u string
		switch entity.Type(eType) {
		case entity.TypeInstance:
			u = api.NewURL().Project(projectName).Path("1.0", "instances", eName).String()
		case entity.TypeProfile:
			u = api.NewURL().Project(projectName).Path("1.0", "profiles", eName).String()
		default:
			return fmt.Errorf("Unexpected entity type in placement ruleset usage query")
		}

		projectMap, ok := urlMap[projectName]
		if !ok {
			urlMap[projectName] = map[string][]string{
				rulesetName: {u},
			}

			return nil
		}

		projectMap[rulesetName] = append(projectMap[rulesetName], u)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve used by URLs for placement rulesets: %w", err)
	}

	return urlMap, nil
}
