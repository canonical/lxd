//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"time"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t instances.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e instance objects version=2
//go:generate mapper stmt -e instance objects-by-ID version=2
//go:generate mapper stmt -e instance objects-by-Project version=2
//go:generate mapper stmt -e instance objects-by-Project-and-Type version=2
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Node version=2
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Node-and-Name version=2
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Name version=2
//go:generate mapper stmt -e instance objects-by-Project-and-Name version=2
//go:generate mapper stmt -e instance objects-by-Project-and-Name-and-Node version=2
//go:generate mapper stmt -e instance objects-by-Project-and-Node version=2
//go:generate mapper stmt -e instance objects-by-Type version=2
//go:generate mapper stmt -e instance objects-by-Type-and-Name version=2
//go:generate mapper stmt -e instance objects-by-Type-and-Name-and-Node version=2
//go:generate mapper stmt -e instance objects-by-Type-and-Node version=2
//go:generate mapper stmt -e instance objects-by-Node version=2
//go:generate mapper stmt -e instance objects-by-Node-and-Name version=2
//go:generate mapper stmt -e instance objects-by-Name version=2
//go:generate mapper stmt -e instance id version=2
//go:generate mapper stmt -e instance create version=2
//go:generate mapper stmt -e instance rename version=2
//go:generate mapper stmt -e instance delete-by-Project-and-Name version=2
//go:generate mapper stmt -e instance update version=2
//
//go:generate mapper method -i -e instance GetMany references=Config,Device version=2
//go:generate mapper method -i -e instance GetOne version=2
//go:generate mapper method -i -e instance ID version=2
//go:generate mapper method -i -e instance Exists version=2
//go:generate mapper method -i -e instance Create references=Config,Device version=2
//go:generate mapper method -i -e instance Rename version=2
//go:generate mapper method -i -e instance DeleteOne-by-Project-and-Name version=2
//go:generate mapper method -i -e instance Update references=Config,Device version=2

// Instance is a value object holding db-related details about an instance.
type Instance struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Name         string `db:"primary=yes"`
	Node         string `db:"join=nodes.name"`
	Type         instancetype.Type
	Snapshot     bool `db:"ignore"`
	Architecture int
	Ephemeral    bool
	CreationDate time.Time
	Stateful     bool
	LastUseDate  sql.NullTime
	Description  string `db:"coalesce=''"`
	ExpiryDate   sql.NullTime
}

// InstanceFilter specifies potential query parameter fields.
type InstanceFilter struct {
	ID      *int
	Project *string
	Name    *string
	Node    *string
	Type    *instancetype.Type
}

// ToAPI converts the database Instance to API type.
func (i *Instance) ToAPI(ctx context.Context, tx *sql.Tx) (*api.Instance, error) {
	profiles, err := GetInstanceProfiles(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	apiProfiles := make([]api.Profile, 0, len(profiles))
	profileNames := make([]string, 0, len(profiles))
	for _, p := range profiles {
		apiProfile, err := p.ToAPI(ctx, tx)
		if err != nil {
			return nil, err
		}

		apiProfiles = append(apiProfiles, *apiProfile)
		profileNames = append(profileNames, p.Name)
	}

	devices, err := GetInstanceDevices(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	apiDevices := DevicesToAPI(devices)
	expandedDevices := ExpandInstanceDevices(config.NewDevices(apiDevices), apiProfiles)

	config, err := GetInstanceConfig(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	expandedConfig := ExpandInstanceConfig(config, apiProfiles)

	archName, err := osarch.ArchitectureName(i.Architecture)
	if err != nil {
		return nil, err
	}

	return &api.Instance{
		InstancePut: api.InstancePut{
			Architecture: archName,
			Config:       config,
			Devices:      apiDevices,
			Ephemeral:    i.Ephemeral,
			Profiles:     profileNames,
			Stateful:     i.Stateful,
			Description:  i.Description,
		},
		CreatedAt:       i.CreationDate,
		ExpandedConfig:  expandedConfig,
		ExpandedDevices: expandedDevices.CloneNative(),
		Name:            i.Name,
		LastUsedAt:      i.LastUseDate.Time,
		Location:        i.Node,
		Type:            i.Type.String(),
		Project:         i.Project,
	}, nil
}
