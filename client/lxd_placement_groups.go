package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetPlacementGroupNames returns a list of placement group names in the current project.
func (r *ProtocolLXD) GetPlacementGroupNames() ([]string, error) {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := api.NewURL().Path("placement-groups").String()
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNames(baseURL, urls...)
}

// GetPlacementGroupNamesAllProjects returns a map of project name to slice of placement group names.
func (r *ProtocolLXD) GetPlacementGroupNamesAllProjects() (map[string][]string, error) {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := api.NewURL().Path("placement-groups").WithQuery("all-projects", "true").String()
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNamesAllProjects(baseURL, urls...)
}

// GetPlacementGroups returns placement groups in the current project.
func (r *ProtocolLXD) GetPlacementGroups() ([]api.PlacementGroup, error) {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return nil, err
	}

	var placementGroups []api.PlacementGroup
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("placement-groups").WithQuery("recursion", "1").String(), nil, "", &placementGroups)
	if err != nil {
		return nil, err
	}

	return placementGroups, nil
}

// GetPlacementGroupsAllProjects returns the placement groups from all projects.
func (r *ProtocolLXD) GetPlacementGroupsAllProjects() ([]api.PlacementGroup, error) {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return nil, err
	}

	var placementGroups []api.PlacementGroup
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("placement-groups").WithQuery("recursion", "1").WithQuery("all-projects", "true").String(), nil, "", &placementGroups)
	if err != nil {
		return nil, err
	}

	return placementGroups, nil
}

// GetPlacementGroup gets a single placement group.
func (r *ProtocolLXD) GetPlacementGroup(placementGroupName string) (*api.PlacementGroup, string, error) {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return nil, "", err
	}

	var placementGroup api.PlacementGroup
	eTag, err := r.queryStruct(http.MethodGet, api.NewURL().Path("placement-groups", placementGroupName).String(), nil, "", &placementGroup)
	if err != nil {
		return nil, "", err
	}

	return &placementGroup, eTag, nil
}

// CreatePlacementGroup creates a new placement group.
func (r *ProtocolLXD) CreatePlacementGroup(placementGroupsPost api.PlacementGroupsPost) error {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodPost, api.NewURL().Path("placement-groups").String(), placementGroupsPost, "", nil)
	if err != nil {
		return err
	}

	return nil
}

// UpdatePlacementGroup fully overwrites the updatable fields of the placement group.
func (r *ProtocolLXD) UpdatePlacementGroup(placementGroupName string, placementGroupPut api.PlacementGroupPut, ETag string) error {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodPut, api.NewURL().Path("placement-groups", placementGroupName).String(), placementGroupPut, ETag, nil)
	if err != nil {
		return err
	}

	return nil
}

// DeletePlacementGroup deletes the placement group.
func (r *ProtocolLXD) DeletePlacementGroup(placementGroupName string) error {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodDelete, api.NewURL().Path("placement-groups", placementGroupName).String(), nil, "", nil)
	if err != nil {
		return err
	}

	return nil
}

// RenamePlacementGroup renames the placement group.
func (r *ProtocolLXD) RenamePlacementGroup(placementGroupName string, placementGroupPost api.PlacementGroupPost) error {
	err := r.CheckExtension("instance_placement_groups")
	if err != nil {
		return err
	}

	_, err = r.queryStruct(http.MethodPost, api.NewURL().Path("placement-groups", placementGroupName).String(), placementGroupPost, "", nil)
	if err != nil {
		return err
	}

	return nil
}
