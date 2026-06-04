//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
)

// InstancesProfilesRow represents a single row of the instances_profiles table.
// db:model instances_profiles
type InstancesProfilesRow struct {
	ID         int64 `db:"id"`
	InstanceID int64 `db:"instance_id"`
	ProfileID  int64 `db:"profile_id"`
	ApplyOrder int64 `db:"apply_order"`
}

// APIName implements [query.APINamer] for API friendly error messages.
func (InstancesProfilesRow) APIName() string {
	return "Instance profile"
}

// GetProfileInstances returns all available Instances for the Profile.
func GetProfileInstances(ctx context.Context, tx *sql.Tx, profileID int) ([]Instance, error) {
	rows, err := query.Select[InstancesProfilesRow](ctx, tx, "WHERE instances_profiles.profile_id = ? ORDER BY instances_profiles.instance_id, instances_profiles.apply_order", profileID)
	if err != nil {
		return nil, err
	}

	result := make([]Instance, len(rows))
	for i, row := range rows {
		instanceID := int(row.InstanceID)
		instances, err := GetInstances(ctx, tx, InstanceFilter{ID: &instanceID})
		if err != nil {
			return nil, err
		}

		result[i] = instances[0]
	}

	return result, nil
}

// GetInstanceProfiles returns all available Profiles for the Instance.
func GetInstanceProfiles(ctx context.Context, tx *sql.Tx, instanceID int) ([]Profile, error) {
	rows, err := query.Select[InstancesProfilesRow](ctx, tx, "WHERE instances_profiles.instance_id = ? ORDER BY instances_profiles.instance_id, instances_profiles.apply_order", instanceID)
	if err != nil {
		return nil, err
	}

	result := make([]Profile, len(rows))
	for i, row := range rows {
		profileID := int(row.ProfileID)
		profiles, err := GetProfiles(ctx, tx, ProfileFilter{ID: &profileID})
		if err != nil {
			return nil, err
		}

		result[i] = profiles[0]
	}

	return result, nil
}

// DeleteInstanceProfiles deletes all instance profile associations for the given instance ID.
func DeleteInstanceProfiles(ctx context.Context, tx *sql.Tx, instanceID int) error {
	_, err := query.DeleteMany[InstancesProfilesRow, *InstancesProfilesRow](ctx, tx, "WHERE instance_id = ?", instanceID)
	return err
}

// UpdateInstanceProfiles updates the profiles of an instance in the order they are given.
func UpdateInstanceProfiles(ctx context.Context, tx *sql.Tx, instanceID int, projectName string, profiles []string) error {
	err := DeleteInstanceProfiles(ctx, tx, instanceID)
	if err != nil {
		return err
	}

	project := projectName
	enabled, err := ProjectHasProfiles(ctx, tx, project)
	if err != nil {
		return fmt.Errorf("Check if project has profiles: %w", err)
	}

	if !enabled {
		project = "default"
	}

	rows := make([]InstancesProfilesRow, 0, len(profiles))
	for i, name := range profiles {
		profileID, err := GetProfileID(ctx, tx, project, name)
		if err != nil {
			return err
		}

		rows = append(rows, InstancesProfilesRow{
			InstanceID: int64(instanceID),
			ProfileID:  profileID,
			ApplyOrder: int64(i + 1),
		})
	}

	return query.CreateMany[InstancesProfilesRow](ctx, tx, rows)
}
