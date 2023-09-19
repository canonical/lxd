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

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
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

// GetDeployments returns the deployments that match the filter parameters.
// If there are no deployments, it returns an empty list and no error.
// Accepts filters for narrowing down the results returned.
func (c *ClusterTx) GetDeployments(ctx context.Context, filters ...DeploymentFilter) ([]*Deployment, error) {
	var q *strings.Builder = &strings.Builder{}
	var args []any

	q.WriteString(`
	SELECT
		p.name AS project_name,
    	deployments.id,
    	deployments.name AS deployment_name,
    	deployments.description,
    	deployments.governor_webhook_url,
    	dc.key AS deployment_config_key,
    	dc.value AS deployment_config_value
	FROM deployments
	INNER JOIN projects p ON deployments.project_id = p.id
	LEFT JOIN deployment_configs dc ON deployments.id = dc.deployment_id
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
				qFilters = append(qFilters, "p.name = ?")
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
	deploymentsMap := make(map[int64]*Deployment)

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var (
			projectName           string
			deploymentID          int
			deploymentName        string
			deploymentDesc        string
			governorWebhookURL    string
			deploymentConfigKey   sql.NullString
			deploymentConfigValue sql.NullString
		)

		err := scan(
			&projectName,
			&deploymentID,
			&deploymentName,
			&deploymentDesc,
			&governorWebhookURL,
			&deploymentConfigKey,
			&deploymentConfigValue,
		)
		if err != nil {
			return err
		}

		dep, exists := deploymentsMap[int64(deploymentID)]
		if !exists {
			dep = &Deployment{
				Deployment: api.Deployment{
					DeploymentPost: api.DeploymentPost{
						Name: deploymentName,
					},
					DeploymentPut: api.DeploymentPut{
						Description:        deploymentDesc,
						GovernorWebhookURL: governorWebhookURL,
						Config:             make(map[string]string),
					},
				},
				ID:      int64(deploymentID),
				Project: projectName,
			}

			// Populate the new Deployment config map with the first potential entries
			if deploymentConfigKey.Valid && deploymentConfigValue.Valid {
				dep.Config[deploymentConfigKey.String] = deploymentConfigValue.String
			}

			deploymentsMap[int64(deploymentID)] = dep
		} else {
			// Populate the existing Deployment config map
			if deploymentConfigKey.Valid && deploymentConfigValue.Valid {
				dep.Config[deploymentConfigKey.String] = deploymentConfigValue.String
			}
		}

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	// Convert the map to a slice
	deployments := make([]*Deployment, 0, len(deploymentsMap))
	for _, dep := range deploymentsMap {
		deployments = append(deployments, dep)
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

// CreateDeployment creates a new deployment.
func (c *ClusterTx) CreateDeployment(ctx context.Context, projectName string, info *api.DeploymentsPost) (int64, error) {
	// Insert a new deployment record.
	result, err := c.tx.ExecContext(ctx, `
			INSERT INTO deployments (project_id, name, description, governor_webhook_url)
			VALUES ((SELECT id FROM projects WHERE name = ? LIMIT 1), ?, ?, ?)
		`, projectName, info.Name, info.Description, info.GovernorWebhookURL)
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
	sql := "INSERT INTO deployment_configs (deployment_id, key, value) VALUES(?, ?, ?)"
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

// UpdateDeployment updates the deployment with the given ID.
func (c *ClusterTx) UpdateDeployment(ctx context.Context, deploymentID int64, config *api.DeploymentPut) error {
	_, err := c.tx.Exec(`
			UPDATE deployments
			SET description=?, governor_webhook_url=?
			WHERE id=?
		`, config.Description, config.GovernorWebhookURL, deploymentID)
	if err != nil {
		return err
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM deployment_configs WHERE deployment_id=?", deploymentID)
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

	uris := make([]string, 0, len(names))
	for _, name := range names {
		uris = append(uris, api.NewURL().Path(version.APIVersion, "deployments", name).Project(projectName).String())
	}

	return uris, nil
}

// DeploymentKey represents a database deployment key record.
type DeploymentKey struct {
	api.DeploymentKey

	ID         int64
	Project    string
	Deployment string
}

// DeploymentKeyFilter used for filtering deployment keys with GetDeploymentKeys().
type DeploymentKeyFilter struct {
	ProjectName       *string
	DeploymentName    *string
	DeploymentKeyName *string
	CertificateID     *int
}

// GetDeploymentKey returns the deployment keys that match the filter parameters.
func (c *ClusterTx) GetDeploymentKeys(ctx context.Context, filters ...DeploymentKeyFilter) ([]*DeploymentKey, error) {
	var q *strings.Builder = &strings.Builder{}
	var args []any

	q.WriteString(`
	SELECT
		p.name AS project_name,
		d.name AS deployment_name,
		dk.id AS deployment_key_id,
		dk.name AS deployment_key_name,
		dk.description AS deployment_key_description,
		c.fingerprint,
		dk.role AS deployment_key_role
	FROM deployment_keys dk
	INNER JOIN deployments d ON d.id = dk.deployment_id
	INNER JOIN projects p ON p.id = d.project_id
	INNER JOIN certificates c ON c.id = dk.certificate_id
	`)

	if len(filters) > 0 {
		if len(args) == 0 {
			q.WriteString("WHERE (")
		} else {
			q.WriteString("AND (")
		}

		for i, filter := range filters {
			var qFilters []string

			if filter.ProjectName != nil {
				qFilters = append(qFilters, "p.name = ?")
				args = append(args, *filter.ProjectName)
			}

			if filter.DeploymentName != nil {
				qFilters = append(qFilters, "d.name = ?")
				args = append(args, *filter.DeploymentName)
			}

			if filter.DeploymentKeyName != nil {
				qFilters = append(qFilters, "dk.name = ?")
				args = append(args, *filter.DeploymentKeyName)
			}

			if filter.CertificateID != nil {
				qFilters = append(qFilters, "c.id = ?")
				args = append(args, *filter.CertificateID)
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
	deploymentKeys := make([]*DeploymentKey, 0)

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var (
			projectName            string
			deploymentName         string
			deploymentKeyID        int
			deploymentKeyName      string
			deploymentKeyDesc      string
			certificateFingerprint string
			deploymentKeyRole      string
		)

		err := scan(
			&projectName,
			&deploymentName,
			&deploymentKeyID,
			&deploymentKeyName,
			&deploymentKeyDesc,
			&certificateFingerprint,
			&deploymentKeyRole,
		)
		if err != nil {
			return err
		}

		deploymentKeys = append(
			deploymentKeys,
			&DeploymentKey{
				ID:         int64(deploymentKeyID),
				Project:    projectName,
				Deployment: deploymentName,
				DeploymentKey: api.DeploymentKey{
					DeploymentKeyPost: api.DeploymentKeyPost{
						Name: deploymentKeyName,
					},
					DeploymentKeyPut: api.DeploymentKeyPut{
						Description: deploymentKeyDesc,
						Role:        deploymentKeyRole,
					},
					CertificateFingerprint: certificateFingerprint,
				},
			},
		)

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return deploymentKeys, nil
}

// CreateDeploymentKey creates a new deployment key.
func (c *ClusterTx) CreateDeploymentKey(ctx context.Context, projectName string, deploymentName string, info *api.DeploymentKeysPost) (int64, error) {
	result, err := c.tx.ExecContext(
		ctx,
		`
		INSERT INTO deployment_keys (deployment_id, name, description, certificate_id, role)
		SELECT
			(SELECT id FROM deployments d WHERE d.name = ?) AS deployment_id,
			? AS name,
			? AS description,
			(SELECT id FROM certificates WHERE fingerprint = ?) AS certificate_id,
			? AS role;
		`,
		deploymentName,
		info.Name,
		info.Description,
		info.CertificateFingerprint,
		info.Role,
	)
	if err != nil {
		return -1, fmt.Errorf("Failed inserting deployment key record: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, err
	}

	return id, nil
}

// UpdateDeploymentKey updates the deployment key with the given name.
func (c *ClusterTx) UpdateDeploymentKey(ctx context.Context, projectName string, deploymentName string, deploymentKeyName string, info *api.DeploymentKeyPut) error {
	_, err := c.tx.Exec(`
	UPDATE deployment_keys
	SET description = ?, role = ?
	WHERE deployment_id IN (
		SELECT d.id
		FROM deployments d
		INNER JOIN projects p ON p.id = d.project_id
		WHERE p.name = ? AND d.name = ?
	) AND name = ?
		`, info.Description, info.Role, projectName, deploymentName, deploymentKeyName)
	if err != nil {
		return err
	}

	return nil
}

// RenameDeployment renames a deployment.
func (c *ClusterTx) RenameDeploymentKey(ctx context.Context, deploymentKeyID int64, newDeploymentKeyName string) error {
	_, err := c.tx.ExecContext(ctx, "UPDATE deployment_keys SET name=? WHERE id=?", newDeploymentKeyName, deploymentKeyID)
	return err
}

// DeleteDeploymentKey deletes the deployment key with the given name.
func (c *ClusterTx) DeleteDeploymentKey(ctx context.Context, projectName string, deploymentName string, deploymentKeyName string) error {
	res, err := c.tx.Exec(`
	DELETE FROM deployment_keys
	WHERE deployment_id IN (
		SELECT d.id
		FROM deployments d
		INNER JOIN projects p ON p.id = d.project_id
		WHERE p.name = ? AND d.name = ?
	) AND name = ?
		`, projectName, deploymentName, deploymentKeyName)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "Deployment key %q not found", deploymentKeyName)
	}

	return nil
}

// DeploymentShapeFilter used for filtering deployment shape GetDeploymentShapes().
type DeploymentShapeFilter struct {
	Name *string
}

// DeploymentShape represents a database deployment shape.
type DeploymentShape struct {
	api.DeploymentShape

	ID int64
}

// GetDeploymentShapes returns all DeploymentShapes for a given Deployment ID.
// If there are no deployment shapes, it returns an empty list and no error.
// Accepts filters for narrowing down the results returned.
func (c *ClusterTx) GetDeploymentShapes(ctx context.Context, deploymentID int64, filters ...DeploymentShapeFilter) ([]*DeploymentShape, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []any{deploymentID}

	q.WriteString(`
	SELECT
		s.id,
		s.name,
		s.description,
		s.scaling_minimum,
		s.scaling_maximum,
		(
			SELECT COUNT(*)
    		FROM deployment_shape_instances AS dsi
    		WHERE dsi.deployment_shape_id = s.id
		),
		s.instance_template,
		sc.key AS deployment_shape_config_key,
		sc.value AS deployment_shape_config_value
	FROM deployment_shapes AS s
	LEFT JOIN deployment_shape_configs sc ON s.id = sc.deployment_shape_id
	WHERE deployment_id = ?
	`)

	if len(filters) > 0 {
		q.WriteString("AND (")

		for i, filter := range filters {
			var qFilters []string

			if filter.Name != nil {
				qFilters = append(qFilters, "name = ?")
				args = append(args, *filter.Name)
			}

			if qFilters == nil {
				return nil, fmt.Errorf("Invalid deployment shape filter")
			}

			if i > 0 {
				q.WriteString(" OR ")
			}

			q.WriteString(fmt.Sprintf("(%s)", strings.Join(qFilters, " AND ")))
		}

		q.WriteString(")")
	}

	var err error
	deploymentShapesMap := make(map[int64]*DeploymentShape)

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var deploymentShape DeploymentShape
		var instTemplateJSON string
		var deploymentShapeConfigKey sql.NullString
		var deploymentShapeConfigValue sql.NullString

		err := scan(
			&deploymentShape.ID,
			&deploymentShape.Name,
			&deploymentShape.Description,
			&deploymentShape.ScalingMinimum,
			&deploymentShape.ScalingMaximum,
			&deploymentShape.ScalingCurrent,
			&instTemplateJSON,
			&deploymentShapeConfigKey,
			&deploymentShapeConfigValue,
		)
		if err != nil {
			return err
		}

		depShape, exists := deploymentShapesMap[int64(deploymentShape.ID)]
		if !exists {
			err = json.Unmarshal([]byte(instTemplateJSON), &deploymentShape.InstanceTemplate)
			if err != nil {
				return fmt.Errorf("Failed unmarshalling instance template: %w", err)
			}

			depShape = &DeploymentShape{
				DeploymentShape: api.DeploymentShape{
					DeploymentShapePost: api.DeploymentShapePost{
						Name: deploymentShape.Name,
					},
					DeploymentShapePut: api.DeploymentShapePut{
						Description:      deploymentShape.Description,
						Config:           make(map[string]string),
						InstanceTemplate: deploymentShape.InstanceTemplate,
						ScalingMinimum:   deploymentShape.ScalingMinimum,
						ScalingMaximum:   deploymentShape.ScalingMaximum,
					},
					ScalingCurrent: deploymentShape.ScalingCurrent,
				},
				ID: int64(deploymentShape.ID),
			}

			// Populate the Deployment config map with the first potential entries
			if deploymentShapeConfigKey.Valid && deploymentShapeConfigValue.Valid {
				depShape.Config[deploymentShapeConfigKey.String] = deploymentShapeConfigValue.String
			}

			deploymentShapesMap[int64(deploymentShape.ID)] = depShape
		} else {
			// Populate the existing deployment config map with the potential entries
			if deploymentShapeConfigKey.Valid && deploymentShapeConfigValue.Valid {
				depShape.Config[deploymentShapeConfigKey.String] = deploymentShapeConfigValue.String
			}
		}

		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	// Convert the map to a slice
	deploymentShapes := make([]*DeploymentShape, 0, len(deploymentShapesMap))
	for _, depShape := range deploymentShapesMap {
		deploymentShapes = append(deploymentShapes, depShape)
	}

	return deploymentShapes, nil
}

// GetDeploymentShape returns the deployment shape for the given Deployment ID and Name.
func (c *ClusterTx) GetDeploymentShape(ctx context.Context, deploymentID int64, deploymentShapeName string) (*DeploymentShape, error) {
	filters := []DeploymentShapeFilter{{
		Name: &deploymentShapeName,
	}}

	deploymentShapes, err := c.GetDeploymentShapes(ctx, deploymentID, filters...)
	deploymentShapesLen := len(deploymentShapes)
	if (err == nil && deploymentShapesLen <= 0) || errors.Is(err, sql.ErrNoRows) {
		return nil, api.StatusErrorf(http.StatusNotFound, "Deployment shape not found")
	} else if err == nil && deploymentShapesLen > 1 {
		return nil, api.StatusErrorf(http.StatusConflict, "More than one deployment shape found")
	} else if err != nil {
		return nil, err
	}

	return deploymentShapes[0], nil
}

// CreateDeploymentShape creates a new DeploymentShape.
func (c *ClusterTx) CreateDeploymentShape(ctx context.Context, deploymentID int64, info api.DeploymentShapesPost) (int64, error) {
	var err error
	instTemplateJSON, err := json.Marshal(info.InstanceTemplate)
	if err != nil {
		return -1, fmt.Errorf("Failed marshalling instance template: %w", err)
	}

	// Insert a new deployment shape record.
	result, err := c.tx.ExecContext(ctx, `
		INSERT INTO deployment_shapes
		(deployment_id, name, description, scaling_minimum, scaling_maximum, instance_template)
		VALUES (?, ?, ?, ?, ?, ?)
		`, deploymentID, info.Name, info.Description, info.ScalingMinimum, info.ScalingMaximum, instTemplateJSON)
	if err != nil {
		var dqliteErr dqliteDriver.Error
		// Detect SQLITE_CONSTRAINT_UNIQUE (2067) errors.
		if errors.As(err, &dqliteErr) && dqliteErr.Code == 2067 {
			return -1, api.StatusErrorf(http.StatusConflict, "A deployment shape for that name already exists")
		}

		return -1, err
	}

	deploymentShapeID, err := result.LastInsertId()
	if err != nil {
		return -1, err
	}

	err = deploymentShapeConfigAdd(ctx, c.tx, deploymentShapeID, info.Config)
	if err != nil {
		return -1, err
	}

	return deploymentShapeID, err
}

// deploymentShapeConfigAdd inserts deployment shape config keys.
func deploymentShapeConfigAdd(ctx context.Context, tx *sql.Tx, deploymentShapeID int64, config map[string]string) error {
	sql := "INSERT INTO deployment_shape_configs (deployment_shape_id, key, value) VALUES(?, ?, ?)"
	stmt, err := tx.PrepareContext(ctx, sql)
	if err != nil {
		return err
	}

	defer func() { _ = stmt.Close() }()

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.ExecContext(ctx, deploymentShapeID, k, v)
		if err != nil {
			return fmt.Errorf("Failed inserting config: %w", err)
		}
	}

	return nil
}

// UpdateDeploymentShape updates an existing deployment shape.
func (c *ClusterTx) UpdateDeploymentShape(ctx context.Context, deploymentID int64, deploymentShapeID int64, info api.DeploymentShapePut) error {
	instTemplateJSON, err := json.Marshal(info.InstanceTemplate)
	if err != nil {
		return fmt.Errorf("Failed marshalling instance template: %w", err)
	}

	// Update existing deployment shape record.
	res, err := c.tx.ExecContext(ctx, `
		UPDATE deployment_shapes
		SET description = ?, scaling_minimum = ?, scaling_maximum = ?, instance_template = ?
		WHERE deployment_id = ? and id = ?
		`, info.Description, info.ScalingMinimum, info.ScalingMaximum, instTemplateJSON, deploymentID, deploymentShapeID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "DeploymentShape not found")
	}

	_, err = c.tx.ExecContext(ctx, "DELETE FROM deployment_shape_configs WHERE deployment_shape_id=?", deploymentShapeID)
	if err != nil {
		return err
	}

	err = deploymentShapeConfigAdd(ctx, c.tx, deploymentShapeID, info.Config)
	if err != nil {
		return err
	}

	return nil
}

// DeleteDeploymentShape deletes an existing deployment shape.
func (c *ClusterTx) DeleteDeploymentShape(ctx context.Context, deploymentID int64, deploymentShapeID int64) error {
	// Delete existing shape record.
	res, err := c.tx.ExecContext(ctx, `
			DELETE FROM deployment_shapes
			WHERE deployment_id = ? and id = ?
		`, deploymentID, deploymentShapeID)
	if err != nil {
		return err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected <= 0 {
		return api.StatusErrorf(http.StatusNotFound, "DeploymentShape not found")
	}

	return nil
}

// RenameDeploymentShape renames a deployment shape.
func (c *ClusterTx) RenameDeploymentShape(ctx context.Context, deploymentID int64, deploymentShapeID int64, newName string) error {
	_, err := c.tx.ExecContext(ctx, "UPDATE deployment_shapes SET name=? WHERE deployment_id = ? and id = ?", newName, deploymentID, deploymentShapeID)
	return err
}

// DeploymentShapeInstance represents a database deployment shape instance.
type DeploymentShapeInstance struct {
	api.DeploymentInstancesPost

	Instance *cluster.Instance
}

// GetDeploymentShapeInstances returns all instances contained in a DeploymentShape for a given DeploymentShape ID.
func (c *ClusterTx) GetDeploymentShapeInstances(ctx context.Context, deploymentShapeID int64) ([]*DeploymentShapeInstance, error) {
	var q *strings.Builder = &strings.Builder{}
	args := []any{deploymentShapeID}

	q.WriteString(`
	SELECT
		s.name AS deployment_shape_name,
		si.instance_id,
	FROM deployment_shape_instances AS si
	INNER JOIN deployment_shapes s ON s.id = si.deployment_shape_id
	WHERE si.deployment_shape_id = ?
	`)

	var err error
	var deploymentShapeInstances []*DeploymentShapeInstance

	err = query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var deploymentShapeInstance DeploymentShapeInstance

		err := scan(
			&deploymentShapeInstance.ShapeName,
			&deploymentShapeInstance.Instance.ID,
		)
		if err != nil {
			return err
		}

		deploymentShapeInstances = append(deploymentShapeInstances, &deploymentShapeInstance)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return deploymentShapeInstances, nil
}

// CreateDeploymentShapeInstance marks an instance as part of a DeploymentShape.
func (c *ClusterTx) CreateDeploymentShapeInstance(ctx context.Context, deploymentName string, instanceID int64, shapeName string) error {
	_, err := c.tx.ExecContext(ctx, `
		INSERT INTO deployment_shape_instances (deployment_shape_id, instance_id)
		SELECT
			s.id,
			?
		FROM deployment_shapes AS s
		INNER JOIN deployments AS d ON d.id = s.deployment_id
		WHERE d.name = ? AND s.name = ?
		`, instanceID, deploymentName, shapeName)
	if err != nil {
		return err
	}

	return nil
}

// DeploymentInstance represents an instance deployed in a deployment shape.
type DeploymentInstance struct {
	*api.Instance

	DeploymentID        int64
	DeploymentName      string
	DeploymentShapeName string
}

// DeploymentInstanceFilter used for filtering instance in deployment.
type DeploymentInstanceFilter struct {
	ProjectName         *string
	DeploymentName      *string
	DeploymentShapeName *string
}

func (c *ClusterTx) GetDeploymentShapeInstanceIDs(ctx context.Context, filters ...DeploymentInstanceFilter) ([]int64, error) {
	var q *strings.Builder = &strings.Builder{}
	var args []any

	q.WriteString(`
	SELECT instance_id FROM deployment_shape_instances AS di
	INNER JOIN deployment_shapes ds ON ds.id = di.deployment_shape_id
	INNER JOIN deployments d ON d.id = ds.deployment_id
	`)

	if len(filters) > 0 {
		q.WriteString("AND (")

		for i, filter := range filters {
			var qFilters []string

			if filter.DeploymentName != nil {
				qFilters = append(qFilters, "d.name = ?")
				args = append(args, *filter.DeploymentName)
			}

			if filter.DeploymentShapeName != nil {
				qFilters = append(qFilters, "ds.name = ?")
				args = append(args, *filter.DeploymentShapeName)
			}

			if qFilters == nil {
				return nil, fmt.Errorf("Invalid deployment shape filter")
			}

			if i > 0 {
				q.WriteString(" OR ")
			}

			q.WriteString(fmt.Sprintf("(%s)", strings.Join(qFilters, " AND ")))
		}

		q.WriteString(")")
	}

	var deploymentShapeInstanceIDs []int64
	err := query.Scan(ctx, c.tx, q.String(), func(scan func(dest ...any) error) error {
		var deploymentShapeInstanceID int64
		err := scan(&deploymentShapeInstanceID)
		if err != nil {
			return err
		}

		deploymentShapeInstanceIDs = append(deploymentShapeInstanceIDs, deploymentShapeInstanceID)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}

	return deploymentShapeInstanceIDs, nil
}
