package deployments

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// Deployment represents a deployment of instances.
type Deployment struct {
	id          int64
	projectName string
	info        *api.Deployment
	keyInfo     *api.DeploymentKey
	state       *state.State
	permission  DeploymentKeyPermission
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
	info.GovernorWebhookURL = d.info.GovernorWebhookURL
	info.Config = util.CopyConfig(d.info.Config)
	info.UsedBy = nil // To indicate its not populated (use UsedBy() function to populate).

	return &info
}

// UsedBy returns a list of API endpoints referencing this Deployment.
func (d *Deployment) UsedBy() ([]string, error) {
	usedBy := []string{}
	err := d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		instances, err := cluster.GetInstances(ctx, tx.Tx(), cluster.InstanceFilter{Project: &d.projectName})
		if err != nil {
			return err
		}

		filter := db.DeploymentInstanceFilter{
			ProjectName:    &d.projectName,
			DeploymentName: &d.info.Name,
		}

		deploymentShapeInstanceIDs, err := tx.GetDeploymentShapeInstanceIDs(ctx, filter)
		if err != nil {
			return err
		}

		for _, instance := range instances {
			if shared.ValueInSlice(int64(instance.ID), deploymentShapeInstanceIDs) {
				apiInstance := api.Instance{Name: instance.Name}
				usedBy = append(usedBy, apiInstance.URL(version.APIVersion, d.projectName).String())
			}
		}

		// All the deployment keys using this deployment.
		deploymentKeyFilter := db.DeploymentKeyFilter{ProjectName: &d.projectName, DeploymentName: &d.info.Name}
		deploymentKeys, err := tx.GetDeploymentKeys(ctx, deploymentKeyFilter)
		if err != nil {
			return err
		}

		for _, deploymentKey := range deploymentKeys {
			usedBy = append(usedBy, deploymentKey.URL(version.APIVersion, d.projectName, d.info.Name).String())
		}

		return nil
	})
	if err != nil {
		return []string{}, err
	}

	return usedBy, nil
}

// validateName checks name is valid.
func (d *Deployment) validateName(name string) error {
	if name == "" {
		return fmt.Errorf("No deployment name provided")
	}

	if strings.Contains(name, "/") {
		return fmt.Errorf("Deployment names may not contain slashes")
	}

	if strings.Contains(name, " ") {
		return fmt.Errorf("Deployment names may not contain spaces")
	}

	if strings.Contains(name, "_") {
		return fmt.Errorf("Deployment names may not contain underscores")
	}

	if strings.Contains(name, "'") || strings.Contains(name, `"`) {
		return fmt.Errorf("Deployment names may not contain quotes")
	}

	if shared.ValueInSlice(name, []string{".", ".."}) {
		return fmt.Errorf("Invalid deployment name %q", name)
	}

	return nil
}

// validateConfig checks the config and rules are valid.
func (d *Deployment) validateConfig(info *api.DeploymentPut) error {
	for k := range info.Config {
		// Allow user.* configuration.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		return fmt.Errorf("Invalid deployment config option %q", k)
	}

	return nil
}

// Update the deployment.
func (d *Deployment) Update(config *api.DeploymentPut) error {
	err := d.validateConfig(config)
	if err != nil {
		return err
	}

	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateDeployment(ctx, d.id, config)
	})
}

// Rename the deployment.
func (d *Deployment) Rename(newName string) error {
	usedBy, err := d.UsedBy()
	if err != nil {
		return err
	}

	if len(usedBy) > 0 {
		return fmt.Errorf("Cannot rename deployment, it is in use by %v", usedBy)
	}

	err = d.validateName(newName)
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
	usedBy, err := d.UsedBy()
	if err != nil {
		return err
	}

	if len(usedBy) > 0 {
		return fmt.Errorf("Cannot delete deployment, it is in use by %v", usedBy)
	}

	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.DeleteDeployment(ctx, d.id)
	})
}

// DeploymentShapeGet returns a deployment shape.
func (d *Deployment) DeploymentShapeGet(deploymentShapeName string) (deploymentShape *api.DeploymentShape, err error) {
	err = d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbDeploymentShape, err := tx.GetDeploymentShape(ctx, d.id, deploymentShapeName)
		if err != nil {
			return err
		}

		deploymentShape = &dbDeploymentShape.DeploymentShape
		return nil
	})
	if err != nil {
		return nil, err
	}

	return deploymentShape, nil
}

// DeploymentShapeCreate creates a new instance set.
func (d *Deployment) DeploymentShapeCreate(req api.DeploymentShapesPost) error {
	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateDeploymentShape(ctx, d.id, req)
		return err
	})
}

// DeploymentShapeDelete deletes a deployment shape.
func (d *Deployment) DeploymentShapeDelete(deploymentShapeName string) error {
	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		deploymentShape, err := tx.GetDeploymentShape(ctx, d.id, deploymentShapeName)
		if err != nil {
			return err
		}

		return tx.DeleteDeploymentShape(ctx, d.id, deploymentShape.ID)
	})
}

// DeploymentShapeUpdate updates a deployment shape.
func (d *Deployment) DeploymentShapeUpdate(deploymentShapeName string, req api.DeploymentShapePut) error {
	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		deploymentShape, err := tx.GetDeploymentShape(ctx, d.id, deploymentShapeName)
		if err != nil {
			return err
		}

		return tx.UpdateDeploymentShape(ctx, d.id, deploymentShape.ID, req)
	})
}

// DeploymentShapeRename renames a deployment shape.
func (d *Deployment) DeploymentShapeRename(deploymentShapeName string, req api.DeploymentShapePost) error {
	return d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		deploymentShape, err := tx.GetDeploymentShape(ctx, d.id, deploymentShapeName)
		if err != nil {
			return err
		}

		return tx.RenameDeploymentShape(ctx, d.id, deploymentShape.ID, req.Name)
	})
}

// DeploymentKeyPermission represents the permission for a deployment key.
type DeploymentKeyPermission uint8

// AddPermission adds a permission to the DeploymentKeyPermission.
func (p *DeploymentKeyPermission) AddPermission(perm DeploymentKeyPermission) *DeploymentKeyPermission {
	*p |= perm
	return p
}

// RemovePermission removes a permission from the DeploymentKeyPermission.
func (p *DeploymentKeyPermission) RemovePermission(perm DeploymentKeyPermission) *DeploymentKeyPermission {
	*p &^= perm
	return p
}

// HasPermission checks if the DeploymentKeyPermission has a permission.
func (p DeploymentKeyPermission) HasPermission(perm DeploymentKeyPermission) bool {
	return p&perm == perm
}

const (
	// DKRead gives read permission to the deployment key.
	DKRead DeploymentKeyPermission = 1 << iota
	// DKWrite gives write permission to the deployment key.
	DKWrite
)

// validateName checks name is valid.
func (d *Deployment) validateDeploymentKeyName(name string) error {
	if name == "" {
		return fmt.Errorf("No deployment key name provided")
	}

	if strings.Contains(name, "/") {
		return fmt.Errorf("Deployment key names may not contain slashes")
	}

	if strings.Contains(name, " ") {
		return fmt.Errorf("Deployment key names may not contain spaces")
	}

	if strings.Contains(name, "_") {
		return fmt.Errorf("Deployment key names may not contain underscores")
	}

	if strings.Contains(name, "'") || strings.Contains(name, `"`) {
		return fmt.Errorf("Deployment key names may not contain quotes")
	}

	if shared.ValueInSlice(name, []string{".", ".."}) {
		return fmt.Errorf("Invalid deployment key name %q", name)
	}

	return nil
}

// InfoDeploymentKey returns copy of internal info for the DeploymentKey.
func (d *Deployment) InfoDeploymentKey() *api.DeploymentKey {
	// Copy internal info to prevent modification externally.
	info := api.DeploymentKey{}
	info.DeploymentKeyPost = d.keyInfo.DeploymentKeyPost
	info.DeploymentKeyPut = d.keyInfo.DeploymentKeyPut
	info.CertificateFingerprint = d.keyInfo.CertificateFingerprint

	return &info
}

// Permission returns the permission for a deployment.
func (d *Deployment) Permission() DeploymentKeyPermission {
	return d.permission
}

// Rename renames the deployment's key.
func (d *Deployment) RenameDeploymentKey(newName string) error {
	err := d.validateDeploymentKeyName(newName)
	if err != nil {
		return err
	}

	err = d.state.DB.Cluster.Transaction(d.state.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		keys, err := tx.GetDeploymentKeys(ctx, db.DeploymentKeyFilter{
			ProjectName:       &d.projectName,
			DeploymentName:    &d.info.Name,
			DeploymentKeyName: &d.keyInfo.Name,
		})
		if err != nil {
			return err
		}

		if len(keys) == 0 {
			return api.StatusErrorf(http.StatusNotFound, "No keys with that name")
		} else if len(keys) > 1 {
			return api.StatusErrorf(http.StatusTeapot, "Something strange is afoot")
		}

		return tx.RenameDeploymentKey(ctx, keys[0].ID, newName)
	})
	if err != nil {
		return err
	}

	// Apply changes internally.
	d.keyInfo.Name = newName
	return nil
}
