package deployments

import (
	"context"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
)

// Deployment represents a deployment of instances.
type Deployment struct {
	id          int64
	projectName string
	info        *api.Deployment
	state       *state.State
}

// Project returns the project name.
func (d *Deployment) Project() string {
	return d.projectName
}

// Info returns copy of internal info for the Deployment.
func (d *Deployment) Info() *api.Deployment {
	// Copy internal info to prevent modification externally.
	info := api.Deployment{}
	info.Name = d.info.Name
	info.Description = d.info.Description
	info.Config = util.CopyConfig(d.info.Config)
	info.UsedBy = nil // To indicate its not populated (use Usedby() function to populate).

	return &info
}

// UsedBy returns a list of API endpoints referencing this Deployment.
func (d *Deployment) UsedBy() ([]string, error) {
	return nil, nil
}

// validateName checks name is valid.
func (d *Deployment) validateName(name string) error {
	return nil //tomp TODO
}

// validateConfig checks the config and rules are valid.
func (d *Deployment) validateConfig(info *api.DeploymentPut) error {
	return nil //tomp TODO
}

// Update the deployment.
func (d *Deployment) Update(config *api.DeploymentPut) error {
	err := d.validateConfig(config)
	if err != nil {
		return err
	}

	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateDeploment(ctx, d.id, config)
	})
}

// Rename the deployment.
func (d *Deployment) Rename(newName string) error {
	// tomp TODO in use checks.

	err := d.validateName(newName)
	if err != nil {
		return err
	}

	err = d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RenameDeployment(ctx, d.id, newName)
	})
	if err != nil {
		return err
	}

	// Apply changes internally.
	d.info.Name = newName

	return nil
}

// Delete the deployment.
func (d *Deployment) Delete() error {
	// tomp TODO in use checks.

	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteDeployment(ctx, d.id)
	})
}

// InstanceSetCreate creates a new instance set.
func (d *Deployment) InstanceSetCreate(req api.DeploymentInstanceSetsPost) error {
	// tomp TODO validation.

	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateDeploymentInstanceSet(ctx, d.id, req)
		return err
	})
}

// InstanceSetDelete deletes an instance set.
func (d *Deployment) InstanceSetDelete(instSetName string) error {
	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbInstSet, err := tx.GetDeploymentInstanceSet(ctx, d.id, instSetName)
		if err != nil {
			return err
		}

		return tx.DeleteDeploymentInstanceSet(ctx, d.id, dbInstSet.ID)
	})
}

// InstanceSetUpdate updates an instance set.
func (d *Deployment) InstanceSetUpdate(instSetName string, req api.DeploymentInstanceSetPut) error {
	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbInstSet, err := tx.GetDeploymentInstanceSet(ctx, d.id, instSetName)
		if err != nil {
			return err
		}

		return tx.UpdateDeploymentInstanceSet(ctx, d.id, dbInstSet.ID, req)
	})
}

// InstanceSetRename renames an instance set.
func (d *Deployment) InstanceSetRename(instSetName string, req api.DeploymentInstanceSetPost) error {
	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbInstSet, err := tx.GetDeploymentInstanceSet(ctx, d.id, instSetName)
		if err != nil {
			return err
		}

		return tx.RenameDeploymentInstanceSet(ctx, d.id, dbInstSet.ID, req.Name)
	})
}
