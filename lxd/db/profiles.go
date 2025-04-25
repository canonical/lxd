//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
)

// GetProfileNames returns the names of all profiles in the given project.
func (c *ClusterTx) GetProfileNames(ctx context.Context, project string) ([]string, error) {
	q := `
SELECT profiles.name
 FROM profiles
 JOIN projects ON projects.id = profiles.project_id
WHERE projects.name = ?
`
	enabled, err := cluster.ProjectHasProfiles(context.Background(), c.tx, project)
	if err != nil {
		return nil, fmt.Errorf("Check if project has profiles: %w", err)
	}

	if !enabled {
		project = "default"
	}

	profileNames := []string{}
	err = query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var profileName string

		err := scan(&profileName)
		if err != nil {
			return err
		}

		profileNames = append(profileNames, profileName)

		return nil
	}, project)
	if err != nil {
		return nil, err
	}

	return profileNames, nil
}

// GetProfile returns the profile with the given name.
func (c *ClusterTx) GetProfile(ctx context.Context, project, name string) (int64, *api.Profile, error) {
	profiles, err := cluster.GetProfilesIfEnabled(ctx, c.tx, project, []string{name})
	if err != nil {
		return -1, nil, err
	}

	if len(profiles) != 1 {
		return -1, nil, fmt.Errorf("Expected one profile with name %q, got %d profiles", name, len(profiles))
	}

	profile := profiles[0]
	id := int64(profile.ID)

	result, err := profile.ToAPI(ctx, c.tx, nil, nil)
	if err != nil {
		return -1, nil, err
	}

	return id, result, nil
}

// GetProfiles returns the profiles with the given names in the given project.
func (c *ClusterTx) GetProfiles(ctx context.Context, projectName string, profileNames []string) ([]api.Profile, error) {
	profiles := make([]api.Profile, len(profileNames))

	dbProfiles, err := cluster.GetProfilesIfEnabled(ctx, c.tx, projectName, profileNames)
	if err != nil {
		return nil, err
	}

	// Get all the profile configs.
	profileConfigs, err := cluster.GetConfig(ctx, c.Tx(), "profile")
	if err != nil {
		return nil, err
	}

	// Get all the profile devices.
	profileDevices, err := cluster.GetDevices(ctx, c.Tx(), "profile")
	if err != nil {
		return nil, err
	}

	for i, profile := range dbProfiles {
		apiProfile, err := profile.ToAPI(ctx, c.tx, profileConfigs, profileDevices)
		if err != nil {
			return nil, err
		}

		profiles[i] = *apiProfile
	}

	return profiles, nil
}

// GetInstancesWithProfile gets the names of the instance associated with the
// profile with the given name in the given project.
func (c *ClusterTx) GetInstancesWithProfile(ctx context.Context, project, profile string) (map[string][]string, error) {
	q := `SELECT instances.name, projects.name FROM instances
		JOIN instances_profiles ON instances.id == instances_profiles.instance_id
		JOIN projects ON projects.id == instances.project_id
		WHERE instances_profiles.profile_id ==
		  (SELECT profiles.id FROM profiles
		   JOIN projects ON projects.id == profiles.project_id
		   WHERE profiles.name=? AND projects.name=?)`

	results := map[string][]string{}

	enabled, err := cluster.ProjectHasProfiles(context.Background(), c.tx, project)
	if err != nil {
		return nil, fmt.Errorf("Check if project has profiles: %w", err)
	}

	if !enabled {
		project = "default"
	}

	err = query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		var instanceName string
		var projectName string

		err := scan(&instanceName, &projectName)
		if err != nil {
			return err
		}

		if results[projectName] == nil {
			results[projectName] = []string{}
		}

		results[projectName] = append(results[projectName], instanceName)

		return nil
	}, profile, project)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// RemoveUnreferencedProfiles removes unreferenced profiles.
func (c *ClusterTx) RemoveUnreferencedProfiles(ctx context.Context) error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`

	_, err := c.tx.ExecContext(ctx, stmt)

	return err
}
