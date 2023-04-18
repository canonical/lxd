package deployments

import (
	"context"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

// LoadByName loads and initialises a Deployment from the database by project and name.
func LoadByName(s *state.State, projectName string, name string) (*Deployment, error) {
	var err error

	deployment := &Deployment{
		state: s,
	}

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeployment, err := tx.GetDeployment(ctx, projectName, name)
		if err != nil {
			return err
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

// Create validates supplied record and creates new Network ACL record in the database.
func Create(s *state.State, projectName string, deploymentInfo *api.DeploymentsPost) error {
	deployment := Deployment{}

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

	return nil
}
