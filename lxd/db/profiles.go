package db

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t profiles.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e profile names
//go:generate mapper stmt -p db -e profile names-by-Project
//go:generate mapper stmt -p db -e profile names-by-Project-and-Name
//go:generate mapper stmt -p db -e profile objects
//go:generate mapper stmt -p db -e profile objects-by-Project
//go:generate mapper stmt -p db -e profile objects-by-Project-and-Name
//go:generate mapper stmt -p db -e profile config-ref
//go:generate mapper stmt -p db -e profile config-ref-by-Project
//go:generate mapper stmt -p db -e profile config-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e profile devices-ref
//go:generate mapper stmt -p db -e profile devices-ref-by-Project
//go:generate mapper stmt -p db -e profile devices-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e profile used-by-ref
//go:generate mapper stmt -p db -e profile used-by-ref-by-Project
//go:generate mapper stmt -p db -e profile used-by-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e profile id
//go:generate mapper stmt -p db -e profile create struct=Profile
//go:generate mapper stmt -p db -e profile create-config-ref
//go:generate mapper stmt -p db -e profile create-devices-ref
//go:generate mapper stmt -p db -e profile rename
//go:generate mapper stmt -p db -e profile delete
//
//go:generate mapper method -p db -e profile URIs
//go:generate mapper method -p db -e profile List
//go:generate mapper method -p db -e profile Get
//go:generate mapper method -p db -e profile Exists struct=Profile
//go:generate mapper method -p db -e profile ID struct=Profile
//go:generate mapper method -p db -e profile ConfigRef
//go:generate mapper method -p db -e profile DevicesRef
//go:generate mapper method -p db -e profile UsedByRef
//go:generate mapper method -p db -e profile Create struct=Profile
//go:generate mapper method -p db -e profile Rename
//go:generate mapper method -p db -e profile Delete

// Profile is a value object holding db-related details about a profile.
type Profile struct {
	ID          int
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string `db:"coalesce=''"`
	Config      map[string]string
	Devices     map[string]map[string]string
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
	p.Config = profile.Config
	p.Devices = profile.Devices

	return p
}

// ProfileFilter can be used to filter results yielded by ProfileList.
type ProfileFilter struct {
	Project string
	Name    string
}

// Profiles returns a string list of profiles.
func (c *Cluster) Profiles(project string) ([]string, error) {
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
	result, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// ProfileGet returns the profile with the given name.
func (c *Cluster) ProfileGet(project, name string) (int64, *api.Profile, error) {
	var result *api.Profile
	var id int64

	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}

		profile, err := tx.ProfileGet(project, name)
		if err != nil {
			return err
		}

		result = ProfileToAPI(profile)
		id = int64(profile.ID)

		return nil
	})
	if err != nil {
		return -1, nil, err
	}

	return id, result, nil
}

// ProfilesGet returns the profiles with the given names in the given project.
func (c *Cluster) ProfilesGet(project string, names []string) ([]api.Profile, error) {
	profiles := make([]api.Profile, len(names))

	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}

		for i, name := range names {
			profile, err := tx.ProfileGet(project, name)
			if err != nil {
				return errors.Wrapf(err, "Load profile %q", name)
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

// ProfileConfig gets the profile configuration map from the DB.
func (c *Cluster) ProfileConfig(project, name string) (map[string]string, error) {
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

	var key, value string
	query := `
        SELECT
            key, value
        FROM profiles_config
        JOIN profiles ON profiles_config.profile_id=profiles.id
        JOIN projects ON projects.id = profiles.project_id
        WHERE projects.name=? AND profiles.name=?`
	inargs := []interface{}{project, name}
	outfmt := []interface{}{key, value}
	results, err := queryScan(c.db, query, inargs, outfmt)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to get profile '%s'", name)
	}

	if len(results) == 0 {
		/*
		 * If we didn't get any rows here, let's check to make sure the
		 * profile really exists; if it doesn't, let's send back a 404.
		 */
		query := "SELECT id FROM profiles WHERE name=?"
		var id int
		results, err := queryScan(c.db, query, []interface{}{name}, []interface{}{id})
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			return nil, ErrNoSuchObject
		}
	}

	config := map[string]string{}

	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)

		config[key] = value
	}

	return config, nil
}

// ProfileDescriptionUpdate updates the description of the profile with the given ID.
func ProfileDescriptionUpdate(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE profiles SET description=? WHERE id=?", description, id)
	return err
}

// ProfileConfigClear resets the config of the profile with the given ID.
func ProfileConfigClear(tx *sql.Tx, id int64) error {
	_, err := tx.Exec("DELETE FROM profiles_config WHERE profile_id=?", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`DELETE FROM profiles_devices_config WHERE id IN
		(SELECT profiles_devices_config.id
		 FROM profiles_devices_config JOIN profiles_devices
		 ON profiles_devices_config.profile_device_id=profiles_devices.id
		 WHERE profiles_devices.profile_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM profiles_devices WHERE profile_id=?", id)
	if err != nil {
		return err
	}
	return nil
}

// ProfileConfigAdd adds a config to the profile with the given ID.
func ProfileConfigAdd(tx *sql.Tx, id int64, config map[string]string) error {
	str := fmt.Sprintf("INSERT INTO profiles_config (profile_id, key, value) VALUES(?, ?, ?)")
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range config {
		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// ProfileContainersGet gets the names of the containers associated with the
// profile with the given name.
func (c *Cluster) ProfileContainersGet(project, profile string) ([]string, error) {
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

	q := `SELECT containers.name FROM containers JOIN containers_profiles
		ON containers.id == containers_profiles.container_id
		JOIN profiles ON containers_profiles.profile_id == profiles.id
		JOIN projects ON projects.id == profiles.project_id
		WHERE projects.name == ? AND profiles.name == ? AND containers.type == 0`

	results := []string{}
	inargs := []interface{}{project, profile}
	var name string
	outfmt := []interface{}{name}

	output, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return results, err
	}

	for _, r := range output {
		results = append(results, r[0].(string))
	}

	return results, nil
}

// ProfileCleanupLeftover removes unreferenced profiles.
func (c *Cluster) ProfileCleanupLeftover() error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`
	err := exec(c.db, stmt)
	if err != nil {
		return err
	}

	return nil
}

// ProfilesExpandConfig expands the given container config with the config
// values of the given profiles.
func ProfilesExpandConfig(config map[string]string, profiles []api.Profile) map[string]string {
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

// ProfilesExpandDevices expands the given container devices with the devices
// defined in the given profiles.
func ProfilesExpandDevices(devices types.Devices, profiles []api.Profile) types.Devices {
	expandedDevices := types.Devices{}

	// Apply all the profiles
	profileDevices := make([]types.Devices, len(profiles))
	for i, profile := range profiles {
		profileDevices[i] = profile.Devices
	}
	for i := range profileDevices {
		for k, v := range profileDevices[i] {
			expandedDevices[k] = v
		}
	}

	// Stick the given devices on top
	for k, v := range devices {
		expandedDevices[k] = v
	}

	return expandedDevices
}
