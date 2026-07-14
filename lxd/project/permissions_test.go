package project_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

func init() {
	db.StorageRemoteDriverNames = func() []string {
		return []string{"ceph", "cephfs"}
	}
}

// If there's no limit configured on the project, the check passes.
func TestAllowInstanceCreation_NotConfigured(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	info, err := project.FetchProject(tx, "default", true)
	require.NoError(t, err)
	require.Nil(t, info)

	info, err = project.FetchProject(tx, "default", false)
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c1",
		Type: api.InstanceTypeContainer,
	}

	err = project.AllowInstanceCreation(*info, req)
	assert.NoError(t, err)
}

// If a limit is configured and the current number of instances is below it, the check passes.
func TestAllowInstanceCreation_Below(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "5"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
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

	info, err := project.FetchProject(tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = project.AllowInstanceCreation(*info, req)
	assert.NoError(t, err)
}

// If a limit is configured and it matches the current number of instances, the
// check fails.
func TestAllowInstanceCreation_Above(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "1"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
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

	info, err := project.FetchProject(tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = project.AllowInstanceCreation(*info, req)
	assert.EqualError(t, err, `Reached maximum number of instances of type "container" in project "p1"`)
}

// If a limit is configured, but for a different instance type, the check
// passes.
func TestAllowInstanceCreation_DifferentType(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "1"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
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

	info, err := project.FetchProject(tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = project.AllowInstanceCreation(*info, req)
	assert.NoError(t, err)
}

// If a limit is configured, but the limit on instances is more
// restrictive, the check fails.
func TestAllowInstanceCreation_AboveInstances(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.containers": "5", "limits.instances": "1"})
	require.NoError(t, err)

	_, err = cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
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

	info, err := project.FetchProject(tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = project.AllowInstanceCreation(*info, req)
	assert.EqualError(t, err, `Reached maximum number of instances in project "p1"`)
}

// If a direct targeting is blocked, the check fails.
func TestCheckClusterTargetRestriction_RestrictedTrue(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"restricted": "true", "restricted.cluster.target": "block"})
	require.NoError(t, err)

	dbProject, err := cluster.GetProject(ctx, tx.Tx(), "p1")
	require.NoError(t, err)

	p, err := dbProject.ToAPI(ctx, tx.Tx())
	require.NoError(t, err)

	req := &http.Request{}
	authorizer, err := auth.LoadAuthorizer("tls", nil, nil, nil)
	require.NoError(t, err)

	err = project.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.EqualError(t, err, "This project doesn't allow cluster member targeting")
}

// If a direct targeting is allowed, the check passes.
func TestCheckClusterTargetRestriction_RestrictedFalse(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"restricted": "false", "restricted.cluster.target": "block"})
	require.NoError(t, err)

	dbProject, err := cluster.GetProject(ctx, tx.Tx(), "p1")
	require.NoError(t, err)

	p, err := dbProject.ToAPI(ctx, tx.Tx())
	require.NoError(t, err)

	req := &http.Request{}
	authorizer, err := auth.LoadAuthorizer("tls", nil, nil, nil)
	require.NoError(t, err)

	err = project.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.NoError(t, err)
}

// A nil req.Config (as used when restoring a volume snapshot) must fall back to the
// volume's current config, rather than dropping its "size" contribution to the aggregate.
func TestAllowVolumeUpdate_NilConfigFallsBackToCurrentConfig(t *testing.T) {
	c, cleanup := db.NewTestCluster(t)
	defer cleanup()

	ctx := context.Background()
	err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
		if err != nil {
			return err
		}

		return cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.disk": "10MiB"})
	})
	require.NoError(t, err)

	poolID, err := c.CreateStoragePool("pool1", "", "dir", nil)
	require.NoError(t, err)

	currentConfig := map[string]string{"size": "5MiB"}
	_, err = c.CreateStoragePoolVolume("p1", "vol1", "", db.StoragePoolVolumeTypeCustom, poolID, currentConfig, db.StoragePoolVolumeContentTypeFS)
	require.NoError(t, err)

	err = c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.AllowVolumeUpdate(tx, "p1", "vol1", api.StorageVolumePut{}, currentConfig)
	})
	assert.NoError(t, err)
}

// A nil req.Config must still enforce limits.disk using the volume's current config,
// rather than silently skipping the volume's own contribution to the aggregate.
func TestAllowVolumeUpdate_NilConfigStillEnforcesLimit(t *testing.T) {
	c, cleanup := db.NewTestCluster(t)
	defer cleanup()

	ctx := context.Background()
	err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
		if err != nil {
			return err
		}

		return cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.disk": "10MiB"})
	})
	require.NoError(t, err)

	poolID, err := c.CreateStoragePool("pool1", "", "dir", nil)
	require.NoError(t, err)

	currentConfig := map[string]string{"size": "8MiB"}
	_, err = c.CreateStoragePoolVolume("p1", "vol1", "", db.StoragePoolVolumeTypeCustom, poolID, currentConfig, db.StoragePoolVolumeContentTypeFS)
	require.NoError(t, err)

	_, err = c.CreateStoragePoolVolume("p1", "vol2", "", db.StoragePoolVolumeTypeCustom, poolID, map[string]string{"size": "8MiB"}, db.StoragePoolVolumeContentTypeFS)
	require.NoError(t, err)

	err = c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.AllowVolumeUpdate(tx, "p1", "vol1", api.StorageVolumePut{}, currentConfig)
	})
	assert.EqualError(t, err, `Failed checking if volume update allowed: Reached maximum aggregate value "10MiB" for "limits.disk" in project "p1"`)
}

// A same-project move to a different pool must not double-count the volume being moved:
// relocating its pre-move (pool, name) entry replaces its aggregate contribution instead
// of adding a second entry for it.
func TestAllowVolumeMove_SameProjectAvoidsDoubleCounting(t *testing.T) {
	c, cleanup := db.NewTestCluster(t)
	defer cleanup()

	ctx := context.Background()
	err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
		if err != nil {
			return err
		}

		return cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.disk": "10MiB"})
	})
	require.NoError(t, err)

	srcPoolID, err := c.CreateStoragePool("src", "", "dir", nil)
	require.NoError(t, err)

	_, err = c.CreateStoragePool("dst", "", "dir", nil)
	require.NoError(t, err)

	_, err = c.CreateStoragePoolVolume("p1", "vol1", "", db.StoragePoolVolumeTypeCustom, srcPoolID, map[string]string{"size": "10MiB"}, db.StoragePoolVolumeContentTypeFS)
	require.NoError(t, err)

	req := api.StorageVolumesPost{
		StorageVolumePut: api.StorageVolumePut{
			Config: map[string]string{"size": "10MiB"},
		},
		Name: "vol1",
	}

	// Relocating the volume's pre-move (pool, name) entry rather than duplicating it
	// keeps the project's total usage unchanged, so the same-project move is allowed
	// even though the pre-move and post-move sizes together would exceed the quota.
	err = c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.AllowVolumeMove(tx, "p1", "src", "vol1", "p1", "dst", req)
	})
	assert.NoError(t, err)
}

// A cross-project move must be checked as a plain creation in the target project and
// must not have its aggregate contribution cancelled out by a coincidentally
// same-named volume already present in the target project (regression test for a
// quota bypass via name collision).
func TestAllowVolumeMove_CrossProjectDoesNotCancelTargetVolume(t *testing.T) {
	c, cleanup := db.NewTestCluster(t)
	defer cleanup()

	ctx := context.Background()
	err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "src-proj"})
		if err != nil {
			return err
		}

		dstID, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "dst-proj"})
		if err != nil {
			return err
		}

		return cluster.CreateProjectConfig(ctx, tx.Tx(), dstID, map[string]string{"limits.disk": "100MiB"})
	})
	require.NoError(t, err)

	poolID, err := c.CreateStoragePool("pool1", "", "dir", nil)
	require.NoError(t, err)

	// Source volume "vol" in src-proj, and a coincidentally same-named "vol" already
	// in dst-proj that consumes most of the target project's quota.
	_, err = c.CreateStoragePoolVolume("src-proj", "vol", "", db.StoragePoolVolumeTypeCustom, poolID, map[string]string{"size": "50MiB"}, db.StoragePoolVolumeContentTypeFS)
	require.NoError(t, err)

	_, err = c.CreateStoragePoolVolume("dst-proj", "vol", "", db.StoragePoolVolumeTypeCustom, poolID, map[string]string{"size": "90MiB"}, db.StoragePoolVolumeContentTypeFS)
	require.NoError(t, err)

	// Move src-proj:pool1/vol to dst-proj:pool1/vol2 (renamed so it doesn't collide with
	// dst-proj's existing "vol"). The target project would end up with 90MiB + 50MiB, which
	// exceeds its 100MiB quota, so the move must be rejected. The source volume's (pool, name)
	// identity must NOT cancel out dst-proj's own same-named volume.
	req := api.StorageVolumesPost{
		StorageVolumePut: api.StorageVolumePut{
			Config: map[string]string{"size": "50MiB"},
		},
		Name: "vol2",
	}

	err = c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.AllowVolumeMove(tx, "src-proj", "pool1", "vol", "dst-proj", "pool1", req)
	})
	assert.EqualError(t, err, `Failed checking if volume move allowed: Reached maximum aggregate value "100MiB" for "limits.disk" in project "dst-proj"`)
}
