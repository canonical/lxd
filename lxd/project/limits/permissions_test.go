package limits_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/auth/drivers"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
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

	info, err := limits.FetchProject(context.Background(), tx, "default", true)
	require.NoError(t, err)
	require.Nil(t, info)

	info, err = limits.FetchProject(context.Background(), tx, "default", false)
	require.NoError(t, err)

	req := api.InstancesPost{
		Name: "c1",
		Type: api.InstanceTypeContainer,
	}

	err = limits.AllowInstanceCreation(nil, *info, req)
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

	info, err := limits.FetchProject(context.Background(), tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = limits.AllowInstanceCreation(nil, *info, req)
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

	info, err := limits.FetchProject(context.Background(), tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = limits.AllowInstanceCreation(nil, *info, req)
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

	info, err := limits.FetchProject(context.Background(), tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = limits.AllowInstanceCreation(nil, *info, req)
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

	info, err := limits.FetchProject(context.Background(), tx, "p1", true)
	require.NoError(t, err)
	require.NotNil(t, info)

	err = limits.AllowInstanceCreation(nil, *info, req)
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

	testKeyPair := shared.TestingKeyPair()
	testCertFingerprint := testKeyPair.Fingerprint()
	require.NoError(t, err)

	req := &http.Request{URL: &url.URL{}}
	details := request.RequestorArgs{
		Trusted:  true,
		Username: testCertFingerprint,
		Protocol: api.AuthenticationMethodTLS,
	}

	err = request.SetRequestor(req, func(ctx context.Context, authenticationMethod string, identifier string) (*request.RequestorHookResult, error) {
		return &request.RequestorHookResult{
			IdentityID:   1,
			IdentityType: identity.CertificateClientRestricted{},
			Projects:     []string{dbProject.Name},
		}, nil
	}, details)
	require.NoError(t, err)

	authorizer, err := drivers.LoadAuthorizer(context.Background(), drivers.DriverTLS, logger.Log)
	require.NoError(t, err)

	err = limits.CheckClusterTargetRestriction(req.Context(), authorizer, p, "n1")
	assert.EqualError(t, err, "This project does not allow cluster member targeting")
}

// If a direct targeting is blocked but the user can override it, the check passes.
func TestCheckClusterTargetRestriction_RestrictedTrueWithOverride(t *testing.T) {
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

	req := &http.Request{
		URL: &api.NewURL().Path("1.0", "instances").WithQuery("target", "node01").URL,
	}

	details := request.RequestorArgs{
		Trusted:  true,
		Username: "my-certificate-fingerprint",
		Protocol: api.AuthenticationMethodTLS,
	}

	require.NoError(t, err)

	err = request.SetRequestor(req, func(ctx context.Context, authenticationMethod string, identifier string) (*request.RequestorHookResult, error) {
		return &request.RequestorHookResult{
			IdentityID:   1,
			IdentityType: identity.CertificateClientUnrestricted{},
		}, nil
	}, details)
	require.NoError(t, err)

	authorizer, err := drivers.LoadAuthorizer(context.Background(), drivers.DriverTLS, logger.Log)
	require.NoError(t, err)

	// Unrestricted client certificates can override the cluster target restriction.
	err = limits.CheckClusterTargetRestriction(req.Context(), authorizer, p, "n1")
	assert.NoError(t, err)
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
	authorizer, err := drivers.LoadAuthorizer(context.Background(), drivers.DriverTLS, logger.Log)
	require.NoError(t, err)

	err = limits.CheckClusterTargetRestriction(req.Context(), authorizer, p, "n1")
	assert.NoError(t, err)
}

// A nil req.Config (as used when restoring a volume snapshot) must fall back to the
// volume's current config, rather than dropping its "size" contribution to the aggregate.
func TestAllowVolumeUpdate_NilConfigFallsBackToCurrentConfig(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.disk": "10MiB"})
	require.NoError(t, err)

	poolID, err := tx.CreateStoragePool(ctx, "pool1", "", "dir", nil)
	require.NoError(t, err)

	currentConfig := map[string]string{"size": "5MiB"}
	_, err = tx.CreateStoragePoolVolume(ctx, "p1", "vol1", "", cluster.StoragePoolVolumeTypeCustom, poolID, currentConfig, cluster.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	err = limits.AllowVolumeUpdate(ctx, nil, tx, "p1", "vol1", api.StorageVolumePut{}, currentConfig)
	assert.NoError(t, err)
}

// A nil req.Config must still enforce limits.disk using the volume's current config,
// rather than silently skipping the volume's own contribution to the aggregate.
func TestAllowVolumeUpdate_NilConfigStillEnforcesLimit(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.disk": "10MiB"})
	require.NoError(t, err)

	poolID, err := tx.CreateStoragePool(ctx, "pool1", "", "dir", nil)
	require.NoError(t, err)

	currentConfig := map[string]string{"size": "8MiB"}
	_, err = tx.CreateStoragePoolVolume(ctx, "p1", "vol1", "", cluster.StoragePoolVolumeTypeCustom, poolID, currentConfig, cluster.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	_, err = tx.CreateStoragePoolVolume(ctx, "p1", "vol2", "", cluster.StoragePoolVolumeTypeCustom, poolID, map[string]string{"size": "8MiB"}, cluster.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	err = limits.AllowVolumeUpdate(ctx, nil, tx, "p1", "vol1", api.StorageVolumePut{}, currentConfig)
	assert.EqualError(t, err, `Failed checking if volume update allowed: Reached maximum aggregate value "10MiB" for "limits.disk" in project "p1"`)
}

// A project with only a pool-specific "limits.disk.pool.<name>" quota (and no bare
// "limits.disk") must still have that quota enforced on volume creation.
func TestAllowVolumeCreation_PoolOnlyLimitEnforced(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.disk.pool.pool1": "10MiB"})
	require.NoError(t, err)

	poolID, err := tx.CreateStoragePool(ctx, "pool1", "", "dir", nil)
	require.NoError(t, err)

	_, err = tx.CreateStoragePoolVolume(ctx, "p1", "vol1", "", cluster.StoragePoolVolumeTypeCustom, poolID, map[string]string{"size": "8MiB"}, cluster.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	req := api.StorageVolumesPost{
		StorageVolumePut: api.StorageVolumePut{
			Config: map[string]string{"size": "8MiB"},
		},
		Name: "vol2",
	}

	err = limits.AllowVolumeCreation(ctx, nil, tx, "p1", "pool1", req)
	assert.EqualError(t, err, `Failed checking if volume creation allowed: Reached maximum aggregate value "10MiB" for "limits.disk.pool.pool1" in project "p1"`)
}

// A same-project move to a different pool must not double-count the volume being moved:
// relocating its pre-move (pool, name) entry replaces its aggregate contribution instead
// of adding a second entry for it.
func TestAllowVolumeMove_SameProjectAvoidsDoubleCounting(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	id, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "p1"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"limits.disk": "10MiB"})
	require.NoError(t, err)

	srcPoolID, err := tx.CreateStoragePool(ctx, "src", "", "dir", nil)
	require.NoError(t, err)

	_, err = tx.CreateStoragePool(ctx, "dst", "", "dir", nil)
	require.NoError(t, err)

	_, err = tx.CreateStoragePoolVolume(ctx, "p1", "vol1", "", cluster.StoragePoolVolumeTypeCustom, srcPoolID, map[string]string{"size": "10MiB"}, cluster.StoragePoolVolumeContentTypeFS, time.Now())
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
	err = limits.AllowVolumeMove(ctx, nil, tx, "p1", "src", "vol1", "p1", "dst", req)
	assert.NoError(t, err)
}

// A cross-project move must be checked as a plain creation in the target project and
// must not have its aggregate contribution cancelled out by a coincidentally
// same-named volume already present in the target project (regression test for a
// quota bypass via name collision).
func TestAllowVolumeMove_CrossProjectDoesNotCancelTargetVolume(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()
	_, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "src-proj"})
	require.NoError(t, err)

	dstID, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "dst-proj"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), dstID, map[string]string{"limits.disk": "100MiB"})
	require.NoError(t, err)

	poolID, err := tx.CreateStoragePool(ctx, "pool1", "", "dir", nil)
	require.NoError(t, err)

	// Source volume "vol" in src-proj, and a coincidentally same-named "vol" already
	// in dst-proj that consumes most of the target project's quota.
	_, err = tx.CreateStoragePoolVolume(ctx, "src-proj", "vol", "", cluster.StoragePoolVolumeTypeCustom, poolID, map[string]string{"size": "50MiB"}, cluster.StoragePoolVolumeContentTypeFS, time.Now())
	require.NoError(t, err)

	_, err = tx.CreateStoragePoolVolume(ctx, "dst-proj", "vol", "", cluster.StoragePoolVolumeTypeCustom, poolID, map[string]string{"size": "90MiB"}, cluster.StoragePoolVolumeContentTypeFS, time.Now())
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

	err = limits.AllowVolumeMove(ctx, nil, tx, "src-proj", "pool1", "vol", "dst-proj", "pool1", req)
	assert.EqualError(t, err, `Failed checking if volume move allowed: Reached maximum aggregate value "100MiB" for "limits.disk" in project "dst-proj"`)
}
