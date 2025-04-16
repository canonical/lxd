package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetPlacementRulesetNames returns a list of ruleset names in the current project.
func (r *ProtocolLXD) GetPlacementRulesetNames() ([]string, error) {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := api.NewURL().Path("placement-rulesets").String()
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNames(baseURL, urls...)
}

// GetPlacementRulesetNamesAllProjects returns a map of project name to slice of placement ruleset names.
func (r *ProtocolLXD) GetPlacementRulesetNamesAllProjects() (map[string][]string, error) {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := api.NewURL().Path("placement-rulesets").WithQuery("all-projects", "true").String()
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNamesAllProjects(baseURL, urls...)
}

// GetPlacementRulesets returns placement rulesets in the current project.
func (r *ProtocolLXD) GetPlacementRulesets() ([]api.PlacementRuleset, error) {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return nil, err
	}

	var rulesets []api.PlacementRuleset
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("placement-rulesets").WithQuery("recursion", "1").String(), nil, "", &rulesets)
	if err != nil {
		return nil, err
	}

	return rulesets, nil
}

// GetPlacementRulesetsAllProjects returns the placement rules from all projects.
func (r *ProtocolLXD) GetPlacementRulesetsAllProjects() ([]api.PlacementRuleset, error) {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return nil, err
	}

	var rulesets []api.PlacementRuleset
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("placement-rulesets").WithQuery("recursion", "1").WithQuery("all-projects", "true").String(), nil, "", &rulesets)
	if err != nil {
		return nil, err
	}

	return rulesets, nil
}

// GetPlacementRuleset gets a single placement ruleset.
func (r *ProtocolLXD) GetPlacementRuleset(rulesetName string) (*api.PlacementRuleset, string, error) {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return nil, "", err
	}

	var ruleset api.PlacementRuleset
	eTag, err := r.queryStruct(http.MethodGet, api.NewURL().Path("placement-rulesets", rulesetName).String(), nil, "", &ruleset)
	if err != nil {
		return nil, "", err
	}

	return &ruleset, eTag, nil
}

// CreatePlacementRuleset creates a new placement ruleset.
func (r *ProtocolLXD) CreatePlacementRuleset(placementRulesetsPost api.PlacementRulesetsPost) error {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodPost, api.NewURL().Path("placement-rulesets").String(), placementRulesetsPost, "", nil)
	if err != nil {
		return err
	}

	return nil
}

// UpdatePlacementRuleset fully overwrites the updatable fields of the placement ruleset.
func (r *ProtocolLXD) UpdatePlacementRuleset(rulesetName string, placementRulesetPut api.PlacementRulesetPut, ETag string) error {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodPut, api.NewURL().Path("placement-rulesets", rulesetName).String(), placementRulesetPut, ETag, nil)
	if err != nil {
		return err
	}

	return nil
}

// DeletePlacementRuleset deletes the placement ruleset.
func (r *ProtocolLXD) DeletePlacementRuleset(rulesetName string) error {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodDelete, api.NewURL().Path("placement-rulesets", rulesetName).String(), nil, "", nil)
	if err != nil {
		return err
	}

	return nil
}

// RenamePlacementRuleset renames the placement ruleset.
func (r *ProtocolLXD) RenamePlacementRuleset(rulesetName string, placementRulesetPost api.PlacementRulesetPost) error {
	err := r.CheckExtension("instance_placement_rules")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodPost, api.NewURL().Path("placement-rulesets", rulesetName).String(), placementRulesetPost, "", nil)
	if err != nil {
		return err
	}

	return nil
}
