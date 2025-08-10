//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/canonical/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t profiles.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e profile objects
//go:generate mapper stmt -e profile objects-by-ID
//go:generate mapper stmt -e profile objects-by-Name
//go:generate mapper stmt -e profile objects-by-Project
//go:generate mapper stmt -e profile objects-by-Project-and-Name
//go:generate mapper stmt -e profile id
//go:generate mapper stmt -e profile create
//go:generate mapper stmt -e profile rename
//go:generate mapper stmt -e profile update
//go:generate mapper stmt -e profile delete-by-Project-and-Name
//
//go:generate mapper method -i -e profile ID
//go:generate mapper method -i -e profile GetMany references=Config,Device
//go:generate mapper method -i -e profile GetOne
//go:generate mapper method -i -e profile Create references=Config,Device
//go:generate mapper method -i -e profile Rename
//go:generate mapper method -i -e profile Update references=Config,Device
//go:generate mapper method -i -e profile DeleteOne-by-Project-and-Name
//go:generate goimports -w profiles.mapper.go
//go:generate goimports -w profiles.interface.mapper.go

// Profile is a value object holding db-related details about a profile.
type Profile struct {
	ID          int
	ProjectID   int    `db:"omit=create,update"`
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string `db:"coalesce=''"`
}

// ProfileFilter specifies potential query parameter fields.
type ProfileFilter struct {
	ID      *int
	Project *string
	Name    *string
}

// ToAPI returns a cluster Profile as an API struct.
func (p *Profile) ToAPI(ctx context.Context, tx *sql.Tx, profileConfigs map[int]map[string]string, profileDevices map[int][]Device) (*api.Profile, error) {
	var err error

	var dbConfig map[string]string
	if profileConfigs != nil {
		dbConfig = profileConfigs[p.ID]
		if dbConfig == nil {
			dbConfig = map[string]string{}
		}
	} else {
		dbConfig, err = GetProfileConfig(ctx, tx, p.ID)
		if err != nil {
			return nil, err
		}
	}

	var dbDevices map[string]Device
	if profileDevices != nil {
		dbDevices = map[string]Device{}

		for _, dev := range profileDevices[p.ID] {
			dbDevices[dev.Name] = dev
		}
	} else {
		dbDevices, err = GetProfileDevices(ctx, tx, p.ID)
		if err != nil {
			return nil, err
		}
	}

	profile := &api.Profile{
		Name:        p.Name,
		Description: p.Description,
		Config:      dbConfig,
		Devices:     DevicesToAPI(dbDevices),
		Project:     p.Project,
	}

	return profile, nil
}

// GetProfilesIfEnabled returns the profiles from the given project, or the
// default project if "features.profiles" is not set.
func GetProfilesIfEnabled(ctx context.Context, tx *sql.Tx, projectName string, names []string) ([]Profile, error) {
	enabled, err := ProjectHasProfiles(ctx, tx, projectName)
	if err != nil {
		return nil, err
	}

	if !enabled {
		projectName = "default"
	}

	profiles := make([]Profile, 0, len(names))
	for _, name := range names {
		profile, err := GetProfile(ctx, tx, projectName, name)
		if err != nil {
			return nil, err
		}

		profiles = append(profiles, *profile)
	}

	return profiles, nil
}
