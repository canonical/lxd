package project_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared/api"
)

// If there's no limit configured on the project, the check passes.
func TestAllowInstanceCreation_NotConfigured(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	req := api.InstancesPost{
		Name: "c1",
		Type: api.InstanceTypeContainer,
	}
	err := project.AllowInstanceCreation(tx, "default", req)
	assert.NoError(t, err)
}

// If a limit is configured and the current number of instances is below it, the check passes.
func TestAllowInstanceCreation_Below(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateProject(db.Project{Name: "p1"})
	require.NoError(t, err)
	err = tx.CreateProjectConfig(id, map[string]string{"limits.containers": "5"})
	require.NoError(t, err)

	_, err = tx.CreateInstance(db.Instance{
		Project:      "p1",
		Name:         "c1",
		Type:         instancetype.Container,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.NoError(t, err)
}

// If a limit is configured and it matches the current number of instances, the
// check fails.
func TestAllowInstanceCreation_Above(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateProject(db.Project{Name: "p1"})
	require.NoError(t, err)
	err = tx.CreateProjectConfig(id, map[string]string{"limits.containers": "1"})
	require.NoError(t, err)

	_, err = tx.CreateInstance(db.Instance{
		Project:      "p1",
		Name:         "c1",
		Type:         instancetype.Container,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.EqualError(t, err, `Reached maximum number of instances of type "container" in project "p1"`)
}

// If a limit is configured, but for a different instance type, the check
// passes.
func TestAllowInstanceCreation_DifferentType(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateProject(db.Project{Name: "p1"})
	require.NoError(t, err)
	err = tx.CreateProjectConfig(id, map[string]string{"limits.containers": "1"})
	require.NoError(t, err)

	_, err = tx.CreateInstance(db.Instance{
		Project:      "p1",
		Name:         "vm1",
		Type:         instancetype.VM,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.NoError(t, err)
}

// If a limit is configured, but the limit on instances is more
// restrictive, the check fails
func TestAllowInstanceCreation_AboveInstances(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateProject(db.Project{Name: "p1"})
	require.NoError(t, err)
	err = tx.CreateProjectConfig(id, map[string]string{"limits.containers": "5", "limits.instances": "1"})
	require.NoError(t, err)

	_, err = tx.CreateInstance(db.Instance{
		Project:      "p1",
		Name:         "c1",
		Type:         instancetype.Container,
		Architecture: 1,
		Node:         "none",
	})
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c2",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(tx, "p1", req)
	assert.EqualError(t, err, `Reached maximum number of instances in project "p1"`)
}

// If a direct targeting is blocked, the check fails.
func TestCheckClusterTargetRestriction_RestrictedTrue(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateProject(db.Project{Name: "p1"})
	require.NoError(t, err)
	err = tx.CreateProjectConfig(id, map[string]string{"restricted": "true", "restricted.cluster.target": "block"})
	require.NoError(t, err)

	req := &http.Request{}

	err = project.CheckClusterTargetRestriction(tx, req, "p1", "n1")
	assert.EqualError(t, err, "This project doesn't allow cluster member targeting")
}

// If a direct targeting is allowed, the check passes.
func TestCheckClusterTargetRestriction_RestrictedFalse(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	id, err := tx.CreateProject(db.Project{Name: "p1"})
	require.NoError(t, err)
	err = tx.CreateProjectConfig(id, map[string]string{"restricted": "false", "restricted.cluster.target": "block"})
	require.NoError(t, err)

	req := &http.Request{}

	err = project.CheckClusterTargetRestriction(tx, req, "p1", "n1")
	assert.NoError(t, err)
}
