package deployments

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared/api"
)

// LoadByProject loads a list of Deployments from the database by project.
func LoadByProject(s *state.State, projectName string, includeDeploymentKeys bool) ([]*Deployment, error) {
	var err error

	filters := []db.DeploymentFilter{{
		Project: &projectName,
	}}

	deployments := make([]*Deployment, 0)
	var dbDeployments []*db.Deployment
	dbDeploymentKeys := make(map[string]*db.DeploymentKey)
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployments, err = tx.GetDeployments(ctx, filters...)
		if err != nil {
			return err
		}

		if includeDeploymentKeys {
			dkFilter := db.DeploymentKeyFilter{
				ProjectName: &projectName,
			}

			deploymentKeys, err := tx.GetDeploymentKeys(ctx, dkFilter)
			if err != nil {
				return err
			}

			for _, dk := range deploymentKeys {
				dbDeploymentKeys[dk.Deployment] = dk
			}
		}

		for _, dbDeployment := range dbDeployments {
			deployment := &Deployment{
				projectName: projectName,
				state:       s,
				permission:  0,
			}

			deployment.id = dbDeployment.ID
			deployment.info = &dbDeployment.Deployment

			if includeDeploymentKeys {
				dbDeploymentKey, ok := dbDeploymentKeys[dbDeployment.Name]
				if ok {
					deployment.keyInfo = &dbDeploymentKey.DeploymentKey

					// Set the permission based on the role
					if dbDeploymentKey.DeploymentKey.Role == "read-only" {
						deployment.permission.AddPermission(DKRead)
					} else if dbDeploymentKey.DeploymentKey.Role == "admin" {
						deployment.permission.AddPermission(DKRead).AddPermission(DKWrite)
					}
				}
			}

			deployments = append(deployments, deployment)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to load deployments for project %q: %v", projectName, err)
	}

	return deployments, nil
}

// LoadByName loads and initializes a Deployment from the database by project and name.
func LoadByName(s *state.State, projectName string, name string, includeDeploymentKeys bool) (*Deployment, error) {
	var err error

	deployment := &Deployment{
		projectName: projectName,
		state:       s,
		permission:  0,
	}

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployment, err := tx.GetDeployment(ctx, projectName, name)
		if err != nil {
			return err
		}

		if includeDeploymentKeys {
			dkFilter := db.DeploymentKeyFilter{
				ProjectName:    &projectName,
				DeploymentName: &name,
			}

			dbDeploymentKeys, err := tx.GetDeploymentKeys(ctx, dkFilter)
			if err != nil {
				return err
			}

			if len(dbDeploymentKeys) != 1 {
				if len(dbDeploymentKeys) > 1 {
					return fmt.Errorf("There are %d different deployment keys. There should only be one", len(dbDeploymentKeys))
				}

				return fmt.Errorf("Deployment key could not be found for deployment %q", name)
			}

			dbDeploymentKey := dbDeploymentKeys[0]
			deployment.keyInfo = &dbDeploymentKey.DeploymentKey
			// Set the permission based on the role
			if dbDeploymentKey.DeploymentKey.Role == "read-only" {
				deployment.permission.AddPermission(DKRead)
			} else if dbDeploymentKey.DeploymentKey.Role == "admin" {
				deployment.permission.AddPermission(DKRead).AddPermission(DKWrite)
			}
		}

		deployment.id = dbDeployment.ID
		deployment.info = &dbDeployment.Deployment

		return nil
	})
	if err != nil {
		return nil, err
	}

	return deployment, nil
}

// Create validates and creates a new Deployment record in the database.
func Create(s *state.State, projectName string, deploymentInfo *api.DeploymentsPost) error {
	deployment := Deployment{
		projectName: projectName,
		state:       s,
	}

	err := deployment.validateName(deploymentInfo.Name)
	if err != nil {
		return err
	}

	err = deployment.validateConfig(&deploymentInfo.DeploymentPut)
	if err != nil {
		return err
	}

	// Insert DB record.
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err = tx.CreateDeployment(ctx, projectName, deploymentInfo)
		return err
	})
	if err != nil {
		return err
	}

	return nil
}

// CreateDeploymentKey validates and creates a new deployment key in the database.
func CreateDeploymentKey(s *state.State, projectName string, deploymentName string, info *api.DeploymentKeysPost) error {
	deployment, err := LoadByName(s, projectName, deploymentName, false)
	if err != nil {
		return err
	}

	err = deployment.validateDeploymentKeyName(info.Name)
	if err != nil {
		return err
	}

	// Insert DB record.
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateDeploymentKey(ctx, projectName, deploymentName, info)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
