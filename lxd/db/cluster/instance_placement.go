package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/validate"
)

// InstancePlacementRuleKind is the database enum for the api.InstancePlacementRuleKind constants.
type InstancePlacementRuleKind api.InstancePlacementRuleKind

const (
	instancePlacementRuleKindCodeAffinity     int64 = 1
	instancePlacementRuleKindCodeAntiAffinity int64 = 2
	instancePlacementRuleKindCodeNone         int64 = 3
)

// Scan implements sql.Scanner for InstancePlacementRuleKind. This converts the integer value back into the correct API constant or
// returns an error.
func (a *InstancePlacementRuleKind) Scan(value any) error {
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
	case instancePlacementRuleKindCodeAffinity:
		*a = InstancePlacementRuleKind(api.InstancePlacementRuleKindAffinity)
	case instancePlacementRuleKindCodeAntiAffinity:
		*a = InstancePlacementRuleKind(api.InstancePlacementRuleKindAntiAffinity)
	case instancePlacementRuleKindCodeNone:
		*a = InstancePlacementRuleKind(api.InstancePlacementRuleKindNone)
	default:
		return fmt.Errorf("Unknown instance placement rule kind `%d`", ruleInt)
	}

	return nil
}

// Value implements driver.Valuer for InstancePlacementRuleKind. This converts the API constant into an integer or throws an error.
func (a InstancePlacementRuleKind) Value() (driver.Value, error) {
	switch api.InstancePlacementRuleKind(a) {
	case api.InstancePlacementRuleKindAffinity:
		return instancePlacementRuleKindCodeAffinity, nil
	case api.InstancePlacementRuleKindAntiAffinity:
		return instancePlacementRuleKindCodeAntiAffinity, nil
	case api.InstancePlacementRuleKindNone:
		return instancePlacementRuleKindCodeNone, nil
	}

	return nil, fmt.Errorf("Invalid instance placement rule kind %q", a)
}

// InstancePlacementRule is the database representation of an api.InstancePlacementRule.
type InstancePlacementRule struct {
	ID       int
	Name     string
	Kind     InstancePlacementRuleKind
	Priority int
	Required bool
	Selector Selector
}

// ToAPI converts an InstancePlacementRule into an api.InstancePlacementRule.
func (i InstancePlacementRule) ToAPI() (string, api.InstancePlacementRule) {
	return i.Name, api.InstancePlacementRule{
		Required: i.Required,
		Kind:     api.InstancePlacementRuleKind(i.Kind),
		Priority: i.Priority,
		Selector: api.Selector{
			EntityType: string(i.Selector.EntityType),
			Matchers:   i.Selector.Matchers,
		},
	}
}

// InstancePlacementRulesToAPI converts a slice of InstancePlacementRule to a map of api.InstancePlacementRule.
func InstancePlacementRulesToAPI(rules []InstancePlacementRule) map[string]api.InstancePlacementRule {
	apiRules := make(map[string]api.InstancePlacementRule, len(rules))
	for _, rule := range rules {
		name, apiRule := rule.ToAPI()
		apiRules[name] = apiRule
	}

	return apiRules
}

// InstancePlacementRulesFromAPI parses and validates the given map of api.InstancePlacementRule, returning a slice of
// InstancePlacementRule or an error.
func InstancePlacementRulesFromAPI(rules map[string]api.InstancePlacementRule) ([]InstancePlacementRule, error) {
	dbRules := make([]InstancePlacementRule, 0, len(rules))
	for name, rule := range rules {
		i, err := InstancePlacementRuleFromAPI(name, rule)
		if err != nil {
			return nil, err
		}

		dbRules = append(dbRules, *i)
	}

	return dbRules, nil
}

// InstancePlacementRuleFromAPI parses and validates the given rulename and api.InstancePlacementRule, returning an
// InstancePlacementRule or an error.
func InstancePlacementRuleFromAPI(rulename string, rule api.InstancePlacementRule) (*InstancePlacementRule, error) {
	err := validate.IsDeviceName(rulename)
	if err != nil {
		return nil, err
	}

	if rule.Priority < 0 || rule.Priority > 255 {
		return nil, fmt.Errorf("Instance placement rule priority must be in the range 0-255")
	}

	kind := InstancePlacementRuleKind(rule.Kind)
	_, err = kind.Value()
	if err != nil {
		return nil, err
	}

	selectorEntityType := entity.Type(rule.Selector.EntityType)
	if !shared.ValueInSlice(selectorEntityType, []entity.Type{entity.TypeInstance, entity.TypeClusterMember, entity.TypeClusterGroup}) {
		return nil, fmt.Errorf("Instance placement rule selector entity type must be one of %q, %q, or %q", entity.TypeInstance, entity.TypeClusterMember, entity.TypeClusterGroup)
	}

	existingProperties := make([]string, 0, len(rule.Selector.Matchers))
	for _, matcher := range rule.Selector.Matchers {
		if shared.ValueInSlice(matcher.Property, existingProperties) {
			return nil, fmt.Errorf("Duplicate selector matcher property %q found in instance placement rule %q", matcher.Property, rulename)
		}

		switch selectorEntityType {
		case entity.TypeInstance:
			if matcher.Property != "name" && !strings.HasPrefix(matcher.Property, "config.") {
				return nil, fmt.Errorf("Matcher property %q not allowed for entity type %q", matcher.Property, entity.TypeInstance)
			}

		case entity.TypeClusterMember:
			if matcher.Property != "name" && !strings.HasPrefix(matcher.Property, "config.") {
				return nil, fmt.Errorf("Matcher property %q not allowed for entity type %q", matcher.Property, entity.TypeClusterMember)
			}

		case entity.TypeClusterGroup:
			if matcher.Property != "name" {
				return nil, fmt.Errorf("Matcher property %q not allowed for entity type %q", matcher.Property, entity.TypeClusterGroup)
			}
		}

		existingProperties = append(existingProperties, matcher.Property)
	}

	i := &InstancePlacementRule{
		Name:     rulename,
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

// UpsertInstancePlacementRules deletes any existing rules for the given entity type and ID and creates the given rules.
// Rules can only be created for instances and profiles.
// This takes advantage of the identical structure of the tables for instance and profile placement rules.
func UpsertInstancePlacementRules(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int, rules []InstancePlacementRule) error {
	if !shared.ValueInSlice(entityType, []entity.Type{entity.TypeInstance, entity.TypeProfile}) {
		return fmt.Errorf("Cannot set instance placement rules for type %q: Instance placement rules may only be set on instances and profiles", entityType)
	}

	q1 := `DELETE FROM selectors WHERE id = (
	SELECT selector_id FROM placements_selectors
	    JOIN placements ON placements_selectors.placement_id = placements.id
	    JOIN instances_placements ON placements.id = instances_placements.placement_id
	    WHERE instances_placements.instance_id = ?
)`

	q2 := `DELETE FROM placements WHERE id = (
    SELECT placement_id FROM instances_placements WHERE instance_id = ?
)`

	tblPrefix := "instance"
	if entityType == entity.TypeProfile {
		tblPrefix = "profile"
		q1 = strings.ReplaceAll(q1, "instance", tblPrefix)
		q2 = strings.ReplaceAll(q2, "instance", tblPrefix)
	}

	_, err := tx.ExecContext(ctx, q1, entityID)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, q2, entityID)
	if err != nil {
		return err
	}

	for _, rule := range rules {
		ruleID, err := query.UpsertObject(tx, "placements", []string{"kind", "required", "priority"}, []any{rule.Kind, rule.Required, rule.Priority})
		if err != nil {
			return err
		}

		rule.ID = int(ruleID)
		_, err = query.UpsertObject(tx, tblPrefix+"s_placements", []string{tblPrefix + "_id", "name", "placement_id"}, []any{entityID, rule.Name, rule.ID})
		if err != nil {
			return err
		}

		selectorID, err := query.UpsertObject(tx, "selectors", []string{"entity_type", "matchers"}, []any{rule.Selector.EntityType, rule.Selector.Matchers})
		if err != nil {
			return err
		}

		rule.Selector.ID = int(selectorID)
		_, err = query.UpsertObject(tx, "placements_selectors", []string{"placement_id", "selector_id"}, []any{rule.ID, rule.Selector.ID})
		if err != nil {
			return err
		}
	}

	return nil
}

// GetInstancePlacementRulesForEntity gets the instance placement rules for the given entity type and ID.
func GetInstancePlacementRulesForEntity(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int) ([]InstancePlacementRule, error) {
	if !shared.ValueInSlice(entityType, []entity.Type{entity.TypeInstance, entity.TypeProfile}) {
		return nil, fmt.Errorf("Cannot set instance placement rules for type %q: Instance placement rules may only be set on instances and profiles", entityType)
	}

	q := `
SELECT placements.id AS placement_id, instances_placements.name, placements.kind, placements.required, placements.priority, selectors.id AS selector_id, selectors.entity_type, selectors.matchers FROM placements
	JOIN instances_placements ON placements.id = instances_placements.placement_id 
	JOIN instances ON instances.id = instances_placements.instance_id
	JOIN placements_selectors ON placements.id = placements_selectors.placement_id
	JOIN selectors ON placements_selectors.selector_id = selectors.id
	WHERE instances.id = ?
	ORDER BY placements.id, selectors.id
`
	if entityType == entity.TypeProfile {
		q = strings.ReplaceAll(q, "instance", "profile")
	}

	var placements []InstancePlacementRule
	err := query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		var placement InstancePlacementRule
		var selector Selector
		err := scan(&placement.ID, &placement.Name, &placement.Kind, &placement.Required, &placement.Priority, &selector.ID, &selector.EntityType, &selector.Matchers)
		if err != nil {
			return err
		}

		placement.Selector = selector
		placements = append(placements, placement)
		return nil
	}, entityID)
	if err != nil {
		return nil, err
	}

	return placements, nil
}
