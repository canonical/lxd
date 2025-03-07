//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// GetProject returns the project with the given key.
func (db *DB) GetProject(ctx context.Context, projectName string) (*cluster.Project, error) {
	var err error
	var p *cluster.Project
	err = db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		p, err = cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return p, nil
}

// If the project has the profiles feature enabled, we use its own
// profiles to expand the instances configs, otherwise we use the
// profiles from the default project.
func projectInheritsProfiles(project *api.Project) bool {
	return shared.IsFalseOrEmpty(project.Config["features.profiles"])
}

// ProjectArgs is all the instances and profiles associated with a project.
// If Project.Config["features.profiles"] is false, Profiles is the default
// project's profiles.
type ProjectArgs struct {
	Profiles  []api.Profile
	Instances []api.Instance
}

// GetProjectInstancesAndProfiles gets all instances and profiles associated
// with some projects in as few queries as possible.
// Returns a map[projectName]args.
func (c *ClusterTx) GetProjectInstancesAndProfiles(ctx context.Context, projects ...*api.Project) (map[string]*ProjectArgs, error) {
	projectArgs := make(map[string]*ProjectArgs, len(projects))
	for _, project := range projects {
		projectArgs[project.Name] = &ProjectArgs{}
	}

	var defaultProjectName = api.ProjectDefaultName
	profileFilters := make([]cluster.ProfileFilter, 0, len(projects))
	instanceFilters := make([]cluster.InstanceFilter, 0, len(projects))

	for _, project := range projects {
		instanceFilters = append(instanceFilters, cluster.InstanceFilter{
			Project: &project.Name,
		})

		// Include the default project's profiles if any project inherits them.
		if projectInheritsProfiles(project) {
			profileFilters = append(profileFilters, cluster.ProfileFilter{
				Project: &defaultProjectName,
			})
		} else {
			profileFilters = append(profileFilters, cluster.ProfileFilter{
				Project: &project.Name,
			})
		}
	}

	dbProfiles, err := cluster.GetProfiles(ctx, c.tx, profileFilters...)
	if err != nil {
		return nil, fmt.Errorf("Fetch profiles from database: %w", err)
	}

	dbProfileConfigs, err := cluster.GetConfig(ctx, c.tx, "profile")
	if err != nil {
		return nil, fmt.Errorf("Fetch profile configs from database: %w", err)
	}

	dbProfileDevices, err := cluster.GetDevices(ctx, c.tx, "profile")
	if err != nil {
		return nil, fmt.Errorf("Fetch profile devices from database: %w", err)
	}

	var defaultProfiles []api.Profile
	profilesByID := make(map[int]*api.Profile, len(dbProfiles))
	for _, profile := range dbProfiles {
		apiProfile, err := profile.ToAPI(ctx, c.tx, dbProfileConfigs, dbProfileDevices)
		if err != nil {
			return nil, err
		}

		profilesByID[profile.ID] = apiProfile

		if profile.Project == defaultProjectName {
			defaultProfiles = append(defaultProfiles, *apiProfile)
		} else {
			projectArgs[profile.Project].Profiles = append(projectArgs[profile.Project].Profiles, *apiProfile)
		}
	}

	defaultProject, ok := projectArgs[defaultProjectName]
	if ok {
		defaultProject.Profiles = defaultProfiles
	}

	// Add profiles to projects which inherit profiles from the default project
	for _, project := range projects {
		if project.Name != defaultProjectName || !projectInheritsProfiles(project) {
			continue
		}

		projectArgs[project.Name].Profiles = defaultProfiles
	}

	// Get all instances using supplied filter.
	dbInstances, err := cluster.GetInstances(ctx, c.tx, instanceFilters...)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instances: %w", err)
	}

	// Fill instances with config and devices.
	instanceArgs, err := c.InstancesToInstanceArgs(ctx, false, dbInstances...)
	if err != nil {
		return nil, err
	}

	hasSnapshots := false
	for _, inst := range instanceArgs {
		if inst.Snapshot {
			hasSnapshots = true
			break
		}
	}

	err = c.instanceProfilesFillWithProfiles(ctx, hasSnapshots, &instanceArgs, profilesByID)
	if err != nil {
		return nil, err
	}

	for _, instance := range instanceArgs {
		apiInstance, err := instance.ToAPI()
		if err != nil {
			return nil, err
		}

		projectArgs[instance.Project].Instances = append(projectArgs[instance.Project].Instances, *apiInstance)
	}

	return projectArgs, nil
}
