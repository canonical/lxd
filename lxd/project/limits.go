package project

import (
	"fmt"
	"strconv"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/units"
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

	// Add the instance being created.
	instances = append(instances, db.Instance{
		Profiles: req.Profiles,
		Config:   req.Config,
	})

	err = checkAggregateInstanceLimits(tx, project, instances, profiles)
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

// Check that we would not violate the project limits if we were to commit the
// given instances and profiles.
func checkAggregateInstanceLimits(tx *db.ClusterTx, project *api.Project, instances []db.Instance, profiles []db.Profile) error {
	// List of config keys for which we need to check aggregate values
	// across all project instances.
	aggregateKeys := []string{}
	for key := range project.Config {
		if shared.StringInSlice(key, []string{"limits.memory"}) {
			aggregateKeys = append(aggregateKeys, key)
		}
	}
	if len(aggregateKeys) == 0 {
		return nil
	}

	instances = expandInstancesConfig(instances, profiles)

	totals, err := getTotalsAcrossInstances(instances, aggregateKeys)
	if err != nil {
		return err
	}

	for _, key := range aggregateKeys {
		parser := aggregateLimitConfigValueParsers[key]
		max, err := parser(project.Config[key])
		if err != nil {
			return err
		}
		if totals[key] > max {
			return fmt.Errorf(
				"Reached maximum aggregate value %s for %q in project %s",
				project.Config[key], key, project.Name)
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

// Expand the configuration of the given instances, taking the give project
// profiles into account.
func expandInstancesConfig(instances []db.Instance, profiles []db.Profile) []db.Instance {
	expandedInstances := make([]db.Instance, len(instances))

	// Index of all profiles by name.
	profilesByName := map[string]db.Profile{}
	for _, profile := range profiles {
		profilesByName[profile.Name] = profile
	}

	for i, instance := range instances {
		profiles := make([]api.Profile, len(instance.Profiles))

		for j, name := range instance.Profiles {
			profile := profilesByName[name]
			profiles[j] = *db.ProfileToAPI(&profile)
		}

		expandedInstances[i] = instance
		expandedInstances[i].Config = db.ProfilesExpandConfig(instance.Config, profiles)
	}

	return expandedInstances
}

// Sum of the effective instance-level value for the given limits across all
// project instances. If excludeInstance is not the empty string, exclude the
// instance with that name.
func getTotalsAcrossInstances(instances []db.Instance, keys []string) (map[string]int64, error) {
	totals := map[string]int64{}

	for _, key := range keys {
		totals[key] = 0
	}

	for _, instance := range instances {
		limits, err := getInstanceLimits(instance, keys)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			totals[key] += limits[key]
		}
	}

	return totals, nil
}

// Return the effective instance-level values for the limits with the given
// keys.
func getInstanceLimits(instance db.Instance, keys []string) (map[string]int64, error) {
	limits := map[string]int64{}

	for _, key := range keys {
		value, ok := instance.Config[key]
		if !ok || value == "" {
			return nil, fmt.Errorf(
				"Instance %s in project %s has no '%s' config, either directly or via a profile",
				instance.Name, instance.Project, key)
		}
		parser := aggregateLimitConfigValueParsers[key]
		limit, err := parser(value)
		if err != nil {
			return nil, errors.Wrapf(
				err, "Parse '%s' for instance %s in project %s",
				key, instance.Name, instance.Project)
		}
		limits[key] = limit
	}

	return limits, nil
}

var aggregateLimitConfigValueParsers = map[string]func(string) (int64, error){
	"limits.memory": units.ParseByteSizeString,
}
