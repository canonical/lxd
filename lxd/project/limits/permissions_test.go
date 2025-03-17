package limits_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/auth/drivers"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// If there's no limit configured on the project, the check passes.
func TestAllowInstanceCreation_NotConfigured(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	req := api.InstancesPost{
		Name: "c1",
		Type: api.InstanceTypeContainer,
	}

	err := limits.AllowInstanceCreation(context.Background(), nil, tx, "default", req)
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

	err = limits.AllowInstanceCreation(context.Background(), nil, tx, "p1", req)
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

	err = limits.AllowInstanceCreation(context.Background(), nil, tx, "p1", req)
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

	err = limits.AllowInstanceCreation(context.Background(), nil, tx, "p1", req)
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

	err = limits.AllowInstanceCreation(context.Background(), nil, tx, "p1", req)
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
	request.SetCtxValue(req, request.CtxTrusted, true)
	request.SetCtxValue(req, request.CtxProtocol, api.AuthenticationMethodTLS)
	request.SetCtxValue(req, request.CtxUsername, testCertFingerprint)
	request.SetCtxValue(req, request.CtxIdentityInfo, &api.IdentityInfo{
		Identity: api.Identity{
			WithEntitlements:     api.WithEntitlements{},
			AuthenticationMethod: api.AuthenticationMethodTLS,
			Type:                 api.IdentityTypeCertificateClientRestricted,
			Identifier:           testCertFingerprint,
			Name:                 "test-certificate",
			Groups:               nil,
			TLSCertificate:       string(testKeyPair.PublicKey()),
		},
	})
	request.SetCtxValue(req, request.CtxCertificateInfo, &api.Certificate{
		WithEntitlements: api.WithEntitlements{},
		Name:             "test-certificate",
		Type:             api.CertificateTypeClient,
		Restricted:       true,
		Projects:         []string{dbProject.Name},
		Certificate:      string(testKeyPair.PublicKey()),
		Fingerprint:      testCertFingerprint,
	})

	authorizer, err := drivers.LoadAuthorizer(context.Background(), drivers.DriverTLS, logger.Log)
	require.NoError(t, err)

	err = limits.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.EqualError(t, err, "This project doesn't allow cluster member targeting")
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

	req = req.WithContext(context.WithValue(req.Context(), request.CtxProtocol, api.AuthenticationMethodTLS))
	req = req.WithContext(context.WithValue(req.Context(), request.CtxUsername, "my-certificate-fingerprint"))
	req = req.WithContext(context.WithValue(req.Context(), request.CtxTrusted, true))
	req = req.WithContext(context.WithValue(req.Context(), request.CtxIdentityInfo, &api.IdentityInfo{
		Identity: api.Identity{
			AuthenticationMethod: api.AuthenticationMethodTLS,
			Type:                 api.IdentityTypeCertificateClientUnrestricted,
		},
	}))

	authorizer, err := drivers.LoadAuthorizer(context.Background(), drivers.DriverTLS, logger.Log)
	require.NoError(t, err)

	err = limits.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.Nil(t, err)
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

	err = limits.CheckClusterTargetRestriction(authorizer, req, p, "n1")
	assert.NoError(t, err)
}
