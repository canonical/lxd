package project

import (
	"fmt"
	"strconv"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

// CheckLimitsUponInstanceCreation returns an error if any project-specific
// limit is violated when creating a new instance.
func CheckLimitsUponInstanceCreation(tx *db.ClusterTx, projectName string, req api.InstancesPost) error {
	project, profiles, instances, err := fetchProject(tx, projectName)
	if err != nil {
		return err
	}

	err = checkInstanceCountLimit(project, len(instances), req.Type)
	if err != nil {
		return err
	}

	return nil
}

// Check that we have not reached the maximum number of instances for
// this type.
func checkInstanceCountLimit(project *api.Project, instanceCount int, instanceType api.InstanceType) error {
	var key string
	switch instanceType {
	case api.InstanceTypeContainer:
		key = "limits.containers"
	case api.InstanceTypeVM:
		key = "limits.virtual-machines"
	default:
		return fmt.Errorf("Unexpected instance type '%s'", instanceType)
	}
	value, ok := project.Config[key]
	if ok {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 0 {
			return fmt.Errorf("Unexpected '%s' value: '%s'", key, value)
		}
		if instanceCount >= limit {
			return fmt.Errorf(
				"Reached maximum number of instances of type %s in project %s",
				instanceType, project.Name)
		}
	}

	return nil
}

// Fetch the given project from the database along with its profiles and instances.
func fetchProject(tx *db.ClusterTx, projectName string) (*api.Project, []db.Profile, []db.Instance, error) {
	project, err := tx.ProjectGet(projectName)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "Fetch project database object")
	}

	profilesFilter := db.ProfileFilter{}

	// If the project has the profiles feature enabled, we use its own
	// profiles to expand the instances configs, otherwise we use the
	// profiles from the default project.
	if projectName == "default" || project.Config["features.profiles"] == "true" {
		profilesFilter.Project = projectName
	} else {
		profilesFilter.Project = "default"
	}

	profiles, err := tx.ProfileList(profilesFilter)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "Fetch profiles from database")
	}

	instances, err := tx.InstanceList(db.InstanceFilter{Project: projectName})
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "Fetch project instances from database")
	}

	return project, profiles, instances, nil
}
