//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	dqliteDriver "github.com/canonical/go-dqlite/driver"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// DeploymentFilter used for filtering deployments with GetDeployments().
type DeploymentFilter struct {
	Project *string
	Name    *string
}

// Deployment represents a database deployment record.
type Deployment struct {
	api.Deployment

	ID      int64
	Project string
}

// GetDeployments returns all storage buckets.
// If there are no deployments, it returns an empty list and no error.
// Accepts filters for narrowing down the results returned.
func (c *ClusterTx) GetDeployments(ctx context.Context, filters ...DeploymentFilter) ([]*Deployment, error) {
	var q *strings.Builder = &strings.Builder{}
	var args []any

	q.WriteString(`
	SELECT
		projects.name as project,
		deployments.id,
		deployments.name,
		deployments.description
	FROM deployments
	JOIN projects ON projects.id = deployments.project_id
	`)

	if len(filters) > 0 {
		if len(args) == 0 {
			q.WriteString("WHERE (")
		} else {
			q.WriteString("AND (")
		}

		for i, filter := range filters {
			var qFilters []string

			if filter.Project != nil {
				qFilters = append(qFilters, "projects.name = ?")
				args = append(args, *filter.Project)
			}

			if filter.Name != nil {
				qFilters = append(qFilters, "deployments.name = ?")
				args = append(args, *filter.Name)
			}

			if qFilters == nil {
				return nil, fmt.Errorf("Invalid deployment filter")
			}

			if i > 0 {
				q.WriteString(" OR ")
			}

			q.WriteString(fmt.Sprintf("(%s)", strings.Join(qFilters, " AND ")))
		}

		q.WriteString(")")
	}

	var err error
	var deployments []*Deployment

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var deployment Deployment

		err := scan(&deployment.Project, &deployment.ID, &deployment.Name, &deployment.Description)
		if err != nil {
			return err
		}

		deployments = append(deployments, &deployment)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	// Populate config.
	for i := range deployments {
		err = deploymentConfig(ctx, c, deployments[i].ID, &deployments[i].Deployment)
		if err != nil {
			return nil, err
		}
	}

	return deployments, nil
}

// GetDeployment returns the Deployment for the given Project Name and Deployment Name.
func (c *ClusterTx) GetDeployment(ctx context.Context, projectName string, deploymentName string) (*Deployment, error) {
	filters := []DeploymentFilter{{
		Project: &projectName,
		Name:    &deploymentName,
	}}

	deployments, err := c.GetDeployments(ctx, filters...)
	deploymentsLen := len(deployments)
	if (err == nil && deploymentsLen <= 0) || errors.Is(err, sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusNotFound, "Deployment not found")
	} else if err == nil && deploymentsLen > 1 {
		return nil, api.StatusErrorf(http.StatusConflict, "More than one deployment found")
	} else if err != nil {
		return nil, err
	}

	return deployments[0], nil
}

// GetDeploymentNameAndProjectWithID returns the deployment name and project name for the given ID.
func (c *ClusterTx) GetDeploymentNameAndProjectWithID(ctx context.Context, deploymentID int64) (string, string, error) {
	var deploymentName string
	var projectName string

	q := `SELECT deployments.name, projects.name FROM deployments JOIN projects ON projects.id=deployments.project_id WHERE deployments.id=?`

	err := c.tx.QueryRowContext(ctx, q, deploymentID).Scan(&deploymentName, &projectName)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", api.StatusErrorf(http.StatusNotFound, "Deployment not found")
		}

		return "", "", err
	}

	return deploymentName, projectName, nil
}

// deploymentConfig populates the config map of the Deployment with the given ID.
func deploymentConfig(ctx context.Context, tx *ClusterTx, deploymentID int64, deployment *api.Deployment) error {
	q := `
		SELECT key, value
		FROM deployments_config
		WHERE deployment_id=?
	`

	deployment.Config = make(map[string]string)
	return query.Scan(ctx, tx.tx, q, func(scan func(dest ...any) error) error {
		var key, value string

		err := scan(&key, &value)
		if err != nil {
			return err
		}

		_, found := deployment.Config[key]
		if found {
			return fmt.Errorf("Duplicate config row found for key %q for deployment ID %d", key, deploymentID)
		}

		deployment.Config[key] = value

		return nil
	}, deploymentID)
}

// CreateDeployment creates a new deployment.
func (c *ClusterTx) CreateDeployment(ctx context.Context, projectName string, info *api.DeploymentsPost) (int64, error) {
	// Insert a new deployment record.
	result, err := c.tx.ExecContext(ctx, `
			INSERT INTO deployments (project_id, name, description)
			VALUES ((SELECT id FROM projects WHERE name = ? LIMIT 1), ?, ?)
		`, projectName, info.Name, info.Description)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, err
	}

	err = deploymentConfigAdd(ctx, c.tx, id, info.Config)
	if err != nil {
		return -1, err
	}

	return id, err
}

// deploymentConfigAdd inserts deployment config keys.
func deploymentConfigAdd(ctx context.Context, tx *sql.Tx, deploymentID int64, config map[string]string) error {
	sql := "INSERT INTO deployments_config (deployment_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.PrepareContext(ctx, sql)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.ExecContext(ctx, deploymentID, k, v)
		if err != nil {
			return fmt.Errorf("Failed inserting config: %w", err)
		}
	}

	return nil
}

// UpdateDeploment updates the deployment with the given ID.
func (c *ClusterTx) UpdateDeploment(ctx context.Context, deploymentID int64, config *api.DeploymentPut) error {
	_, err := c.tx.Exec(`
			UPDATE deployments
			SET description=?
			WHERE id=?
		`, config.Description, deploymentID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM deployments_config WHERE deployment_id=?", deploymentID)
	if err != nil {
		return err
	}

	err = deploymentConfigAdd(ctx, c.tx, deploymentID, config.Config)
	if err != nil {
		return err
	}

	return nil
}

// RenameDeployment renames a deployment.
func (c *ClusterTx) RenameDeployment(ctx context.Context, deploymentID int64, newName string) error {
	_, err := c.tx.ExecContext(ctx, "UPDATE deployments SET name=? WHERE id=?", newName, deploymentID)
	return err
}

// DeleteDeployment deletes the deployment.
func (c *ClusterTx) DeleteDeployment(ctx context.Context, deploymentID int64) error {
	_, err := c.tx.ExecContext(ctx, "DELETE FROM deployments WHERE id=?", deploymentID)
	return err
}

// GetDeploymentsURIs returns the URIs for the deployments with the given project.
func (c *ClusterTx) GetDeploymentsURIs(ctx context.Context, projectID int, projectName string) ([]string, error) {
	sql := `SELECT deployments.name from deployments WHERE deployments.project_id = ?`

	names, err := query.SelectStrings(ctx, c.tx, sql, projectID)
	if err != nil {
		return nil, fmt.Errorf("Unable to get URIs for deployment: %w", err)
	}

	uris := make([]string, len(names))
	for i := range names {
		uris[i] = api.NewURL().Path(version.APIVersion, "deployments", names[i]).Project(projectName).String()
	}

	return uris, nil
}

// DeploymentInstanceSetFilter used for filtering deployment instance set GetDeploymentInstanceSets().
type DeploymentInstanceSetFilter struct {
	Name *string
}

// DeploymentInstanceSet represents a database deployment instance set.
type DeploymentInstanceSet struct {
	api.DeploymentInstanceSet

	ID int64
}

// GetDeploymentInstanceSets returns all Deloyment Instance Sets for a given Deployment ID.
// If there are no instance sets, it returns an empty list and no error.
// Accepts filters for narrowing down the results returned.
func (c *ClusterTx) GetDeploymentInstanceSets(ctx context.Context, deploymentID int64, filters ...DeploymentInstanceSetFilter) ([]*DeploymentInstanceSet, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []any{deploymentID}

	q.WriteString(`
	SELECT
		deployments_instance_sets.id,
		deployments_instance_sets.name,
		deployments_instance_sets.description,
		deployments_instance_sets.scaling_minimum,
		deployments_instance_sets.scaling_maximum,
		deployments_instance_sets.instance_template,
	FROM deployments_instance_sets
	WHERE deployments_instance_sets.deployment_id = ?
	`)

	if len(filters) > 0 {
		q.WriteString("AND (")

		for i, filter := range filters {
			var qFilters []string

			if filter.Name != nil {
				qFilters = append(qFilters, "deployments_instance_sets.name = ?")
				args = append(args, *filter.Name)
			}

			if qFilters == nil {
				return nil, fmt.Errorf("Invalid deployment instance set filter")
			}

			if i > 0 {
				q.WriteString(" OR ")
			}

			q.WriteString(fmt.Sprintf("(%s)", strings.Join(qFilters, " AND ")))
		}

		q.WriteString(")")
	}

	var err error
	var instanceSets []*DeploymentInstanceSet

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var instSet DeploymentInstanceSet
		var instTemplateJSON string

		err := scan(&instSet.ID, &instSet.Name, &instSet.Description, &instSet.ScalingMinimum, &instSet.ScalingMaximum, &instTemplateJSON)
		if err != nil {
			return err
		}

		err = json.Unmarshal([]byte(instTemplateJSON), &instSet.InstanceTemplate)
		if err != nil {
			return fmt.Errorf("Failed unmarshalling instance template: %w", err)
		}

		instanceSets = append(instanceSets, &instSet)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return instanceSets, nil
}

// GetDeploymentInstanceSet returns the deployment instance set for the given Deployment ID and Name.
func (c *ClusterTx) GetDeploymentInstanceSet(ctx context.Context, deploymentID int64, instSetName string) (*DeploymentInstanceSet, error) {
	filters := []DeploymentInstanceSetFilter{{
		Name: &instSetName,
	}}

	instSets, err := c.GetDeploymentInstanceSets(ctx, deploymentID, filters...)
	instSetsLen := len(instSets)
	if (err == nil && instSetsLen <= 0) || errors.Is(err, sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusNotFound, "Deployment instance set not found")
	} else if err == nil && instSetsLen > 1 {
		return nil, api.StatusErrorf(http.StatusConflict, "More than one deployment instance set found")
	} else if err != nil {
		return nil, err
	}

	return instSets[0], nil
}

// CreateDeploymentInstanceSet creates a new Deployment Instance Set.
func (c *ClusterTx) CreateDeploymentInstanceSet(ctx context.Context, deploymentID int64, info api.DeploymentInstanceSetsPost) (int64, error) {
	var err error
	instTemplateJSON, err := json.Marshal(info.InstanceTemplate)
	if err != nil {
		return -1, fmt.Errorf("Failed marshalling instance template: %w", err)
	}

	// Insert a new Deployment Instance Setrecord.
	result, err := c.tx.ExecContext(ctx, `
		INSERT INTO deployments_instance_sets
		(deployment_id, name, description, scaling_minimum, scaling_maximum, instance_template)
		VALUES (?, ?, ?, ?, ?, ?)
		`, deploymentID, info.Name, info.Description, info.ScalingMinimum, info.ScalingMaximum, instTemplateJSON)
	if err != nil {
		var dqliteErr dqliteDriver.Error
		// Detect SQLITE_CONSTRAINT_UNIQUE (2067) errors.
		if errors.As(err, &dqliteErr) && dqliteErr.Code == 2067 {
			return -1, api.StatusErrorf(http.StatusConflict, "A deployment instance set for that name already exists")
		}

		return -1, err
	}

	instSetID, err := result.LastInsertId()
	if err != nil {
		return -1, err
	}

	return instSetID, err
}

// UpdateDeploymentInstanceSet updates an existing Deployment Instance Set.
func (c *ClusterTx) UpdateDeploymentInstanceSet(ctx context.Context, deploymentID int64, instSetID int64, info api.DeploymentInstanceSetPut) error {
	instTemplateJSON, err := json.Marshal(info.InstanceTemplate)
	if err != nil {
		return fmt.Errorf("Failed marshalling instance template: %w", err)
	}

	// Update existing Deployment Instance Set record.
	res, err := c.tx.ExecContext(ctx, `
		UPDATE deployments_instance_sets
		SET description = ?, scaling_minimum = ?, scaling_maximum = ?, instance_template = ?
		WHERE deployment_id = ? and id = ?
		`, info.Description, info.ScalingMinimum, info.ScalingMaximum, instTemplateJSON, deploymentID, instSetID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Deployment instance set not found")
	}

	return nil
}

// DeleteDeploymentInstanceSet deletes an existing Deployment Instance Set.
func (c *ClusterTx) DeleteDeploymentInstanceSet(ctx context.Context, deploymentID int64, instSetID int64) error {
	// Delete existing Deployment Instance Set record.
	res, err := c.tx.ExecContext(ctx, `
			DELETE FROM deployments_instance_sets
			WHERE deployment_id = ? and id = ?
		`, deploymentID, instSetID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Deployment instance set not found")
	}

	return nil
}

// RenameDeploymentInstanceSet renames a deployment instance set.
func (c *ClusterTx) RenameDeploymentInstanceSet(ctx context.Context, deploymentID int64, instSetID int64, newName string) error {
	_, err := c.tx.ExecContext(ctx, "UPDATE deployments_instance_sets SET name=? WHERE deployment_id = ? and id = ?", newName, deploymentID, instSetID)
	return err
}
