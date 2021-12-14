//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t profiles.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e profile objects
//go:generate mapper stmt -p db -e profile objects-by-ID
//go:generate mapper stmt -p db -e profile objects-by-Project
//go:generate mapper stmt -p db -e profile objects-by-Project-and-Name
//go:generate mapper stmt -p db -e profile id
//go:generate mapper stmt -p db -e profile create struct=Profile
//go:generate mapper stmt -p db -e profile rename
//go:generate mapper stmt -p db -e profile delete-by-Project-and-Name
//go:generate mapper stmt -p db -e profile update struct=Profile
//
//go:generate mapper method -p db -e profile GetMany references=Device,Config
//go:generate mapper method -p db -e profile GetOne
//go:generate mapper method -p db -e profile Exists struct=Profile
//go:generate mapper method -p db -e profile ID struct=Profile
//go:generate mapper method -p db -e profile Create references=Device,Config
//go:generate mapper method -p db -e profile Rename
//go:generate mapper method -p db -e profile DeleteOne-by-Project-and-Name
//go:generate mapper method -p db -e profile Update references=Device,Config

// Profile is a value object holding db-related details about a profile.
type Profile struct {
	ID          int
	ProjectID   int    `db:"omit=create,update"`
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string `db:"coalesce=''"`
	UsedBy      []string
}

// ProfileToAPI is a convenience to convert a Profile db struct into
// an API profile struct.
func ProfileToAPI(profile *Profile) *api.Profile {
	p := &api.Profile{
		Name:   profile.Name,
		UsedBy: profile.UsedBy,
	}
	p.Description = profile.Description
	// TODO: fetch profile device/config and handle errors.

	return p
}

// ProfileFilter specifies potential query parameter fields.
type ProfileFilter struct {
	ID      *int
	Project *string
	Name    *string
}

// GetProfileUsedBy returns all the instances that use the given profile.
func (c *ClusterTx) GetProfileUsedBy(profile Profile) ([]string, error) {
	usedBy := []string{}

	profileInstances, err := c.GetProfileInstances()
	if err != nil {
		return nil, err
	}

	for _, instanceID := range profileInstances[profile.ID] {
		instanceURIs, err := c.GetInstanceURIs(InstanceFilter{ID: &instanceID})
		if err != nil {
			return nil, err
		}

		usedBy = append(usedBy, instanceURIs...)
	}

	return usedBy, nil
}

// GetProjectProfileNames returns slice of profile names keyed on the project they belong to.
func (c *ClusterTx) GetProjectProfileNames() (map[string][]string, error) {
	query := `
	SELECT projects.name, profiles.name
	FROM profiles
	JOIN projects ON projects.id = profiles.project_id
	`

	rows, err := c.tx.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string][]string)

	for rows.Next() {
		var projectName, profileName string

		err = rows.Scan(&projectName, &profileName)
		if err != nil {
			return nil, err
		}

		if res[projectName] == nil {
			res[projectName] = []string{profileName}
		} else {
			res[projectName] = append(res[projectName], profileName)
		}
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetProfileNames returns the names of all profiles in the given project.
func (c *Cluster) GetProfileNames(project string) ([]string, error) {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
SELECT profiles.name
 FROM profiles
 JOIN projects ON projects.id = profiles.project_id
WHERE projects.name = ?
`)
	inargs := []interface{}{project}
	var name string
	outfmt := []interface{}{name}
	result, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// GetProfile returns the profile with the given name.
func (c *Cluster) GetProfile(project, name string) (int64, *api.Profile, error) {
	var result *api.Profile
	var id int64

	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		id, result, err = tx.getProfile(project, name)
		return err
	})
	if err != nil {
		return -1, nil, err
	}

	return id, result, nil
}

// Returns the profile with the given name.
func (c *ClusterTx) getProfile(project, name string) (int64, *api.Profile, error) {
	var result *api.Profile
	var id int64

	enabled, err := c.ProjectHasProfiles(project)
	if err != nil {
		return -1, nil, errors.Wrap(err, "Check if project has profiles")
	}
	if !enabled {
		project = "default"
	}

	profile, err := c.GetProfile(project, name)
	if err != nil {
		return -1, nil, err
	}

	result = ProfileToAPI(profile)
	id = int64(profile.ID)

	return id, result, nil
}

// GetProfiles returns the profiles with the given names in the given project.
func (c *Cluster) GetProfiles(projectName string, profileNames []string) ([]api.Profile, error) {
	profiles := make([]api.Profile, len(profileNames))

	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(projectName)
		if err != nil {
			return errors.Wrapf(err, "Failed checking if project %q has profiles", projectName)
		}

		if !enabled {
			projectName = "default"
		}

		for i, profileName := range profileNames {
			profile, err := tx.GetProfile(projectName, profileName)
			if err != nil {
				return errors.Wrapf(err, "Failed loading profile %q", profileName)
			}

			profiles[i] = *ProfileToAPI(profile)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return profiles, nil
}

// GetInstancesWithProfile gets the names of the instance associated with the
// profile with the given name in the given project.
func (c *Cluster) GetInstancesWithProfile(project, profile string) (map[string][]string, error) {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	q := `SELECT instances.name, projects.name FROM instances
		JOIN instances_profiles ON instances.id == instances_profiles.instance_id
		JOIN projects ON projects.id == instances.project_id
		WHERE instances_profiles.profile_id ==
		  (SELECT profiles.id FROM profiles
		   JOIN projects ON projects.id == profiles.project_id
		   WHERE profiles.name=? AND projects.name=?)`

	results := map[string][]string{}
	inargs := []interface{}{profile, project}
	var name string
	outfmt := []interface{}{name, name}

	output, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range output {
		if results[r[1].(string)] == nil {
			results[r[1].(string)] = []string{}
		}

		results[r[1].(string)] = append(results[r[1].(string)], r[0].(string))
	}

	return results, nil
}

// RemoveUnreferencedProfiles removes unreferenced profiles.
func (c *Cluster) RemoveUnreferencedProfiles() error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`
	err := exec(c, stmt)
	if err != nil {
		return err
	}

	return nil
}

// ExpandInstanceConfig expands the given instance config with the config
// values of the given profiles.
func ExpandInstanceConfig(config map[string]string, profiles []api.Profile) map[string]string {
	expandedConfig := map[string]string{}

	// Apply all the profiles
	profileConfigs := make([]map[string]string, len(profiles))
	for i, profile := range profiles {
		profileConfigs[i] = profile.Config
	}

	for i := range profileConfigs {
		for k, v := range profileConfigs[i] {
			expandedConfig[k] = v
		}
	}

	// Stick the given config on top
	for k, v := range config {
		expandedConfig[k] = v
	}

	return expandedConfig
}

// ExpandInstanceDevices expands the given instance devices with the devices
// defined in the given profiles.
func ExpandInstanceDevices(devices map[string]map[string]string, profiles []api.Profile) map[string]map[string]string {
	expandedDevices := map[string]map[string]string{}

	// Apply all the profiles
	for _, profile := range profiles {
		for k, v := range profile.Devices {
			expandedDevices[k] = v
		}
	}

	// Stick the given devices on top
	for k, v := range devices {
		expandedDevices[k] = v
	}

	return expandedDevices
}
