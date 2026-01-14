package drivers

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/dustinkirkland/golang-petname"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

type tlsSuite struct {
	suite.Suite
	authorizer   auth.Authorizer
	cluster      *db.Cluster
	closeCluster func()
}

func TestTLSSuite(t *testing.T) {
	suite.Run(t, new(tlsSuite))
}

func (s *tlsSuite) SetupSuite() {
	s.cluster, s.closeCluster = db.NewTestCluster(s.T())
	var err error
	s.authorizer, err = LoadAuthorizer(context.Background(), DriverTLS, logger.Log)
	s.Require().NoError(err)
}

func (s *tlsSuite) TearDownSuite() {
	s.closeCluster()
}

func (s *tlsSuite) setupCtx(details request.RequestorArgs) context.Context {
	r := &http.Request{
		RemoteAddr: "127.0.0.1:53423",
	}

	err := request.SetRequestor(r, func(ctx context.Context, authenticationMethod string, identifier string, idpGroups []string) (identityID int, idType identity.Type, authGroups []string, effectiveAuthGroups []string, projects []string, err error) {
		switch identifier {
		case "foo-restricted":
			return 1, identity.CertificateClientRestricted{}, nil, nil, []string{"foo"}, nil
		case "unrestricted":
			return 2, identity.CertificateClientUnrestricted{}, nil, nil, nil, nil
		}

		return -1, nil, nil, nil, nil, fmt.Errorf("Unknown identity %q", identifier)
	}, details)
	s.Require().NoError(err)
	return r.Context()
}

func (s *tlsSuite) TestTLSAuthorizer() {
	type testCase struct {
		id            string
		entityURL     *api.URL
		entitlements  []auth.Entitlement
		expectErr     bool
		expectErrCode int
	}

	// Initial cases represent exceptions to entity types that are not project specific (e.g. cases handled by `allowProjectUnspecificEntityType`).
	cases := []testCase{
		{
			id:        "foo-restricted",
			entityURL: entity.ServerURL(),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanViewResources,
				auth.EntitlementCanViewMetrics,
			},
		},
		{
			id:        "foo-restricted",
			entityURL: entity.ServerURL(),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanViewPermissions,
				auth.EntitlementCanCreateIdentities,
				auth.EntitlementCanCreateGroups,
				auth.EntitlementCanCreateIdentityProviderGroups,
				auth.EntitlementCanCreateStoragePools,
				auth.EntitlementCanCreateProjects,
				auth.EntitlementCanOverrideClusterTargetRestriction,
				auth.EntitlementCanViewEvents,
				auth.EntitlementCanViewWarnings,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:           "foo-restricted",
			entityURL:    entity.IdentityURL(api.AuthenticationMethodTLS, "foo-restricted"),
			entitlements: []auth.Entitlement{auth.EntitlementCanView},
		},
		{
			id:        "foo-restricted",
			entityURL: entity.IdentityURL(api.AuthenticationMethodTLS, "foo-restricted"),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:        "foo-restricted",
			entityURL: entity.IdentityURL(api.AuthenticationMethodTLS, petname.Generate(2, "-")),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanView,
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:           "foo-restricted",
			entityURL:    entity.CertificateURL("foo-restricted"),
			entitlements: []auth.Entitlement{auth.EntitlementCanView},
		},
		{
			id:        "foo-restricted",
			entityURL: entity.CertificateURL("foo-restricted"),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:        "foo-restricted",
			entityURL: entity.CertificateURL(petname.Generate(2, "-")),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanView,
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:        "foo-restricted",
			entityURL: entity.ProjectURL("foo"),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanView,
				auth.EntitlementCanCreateImages,
				auth.EntitlementCanCreateImageAliases,
				auth.EntitlementCanCreateInstances,
				auth.EntitlementCanCreateNetworks,
				auth.EntitlementCanCreateNetworkACLs,
				auth.EntitlementCanCreateNetworkZones,
				auth.EntitlementCanCreateProfiles,
				auth.EntitlementCanCreateStorageVolumes,
				auth.EntitlementCanCreateStorageBuckets,
				auth.EntitlementCanViewEvents,
				auth.EntitlementCanViewOperations,
				auth.EntitlementCanViewMetrics,
			},
		},
		{
			id:        "foo-restricted",
			entityURL: entity.ProjectURL("foo"),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:        "foo-restricted",
			entityURL: entity.ProjectURL(petname.Generate(2, "-")),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
				auth.EntitlementCanView,
				auth.EntitlementCanCreateImages,
				auth.EntitlementCanCreateImageAliases,
				auth.EntitlementCanCreateInstances,
				auth.EntitlementCanCreateNetworks,
				auth.EntitlementCanCreateNetworkACLs,
				auth.EntitlementCanCreateNetworkZones,
				auth.EntitlementCanCreateProfiles,
				auth.EntitlementCanCreateStorageVolumes,
				auth.EntitlementCanCreateStorageBuckets,
				auth.EntitlementCanViewEvents,
				auth.EntitlementCanViewOperations,
				auth.EntitlementCanViewMetrics,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
	}

	// Create cases for all remaining entity types.
	for entityType, entitlements := range auth.EntityTypeToEntitlements {
		var entityURL *api.URL
		var pathArgs []string
		var err error
		for {
			// Get an entity URL for the entity type by increasing the number of path arguments.
			// This is very hacky but this way we don't need to export the "nPathArguments" function from the entity package.
			entityURL, err = entityType.URL("", "", pathArgs...)
			if err == nil {
				break
			}

			pathArgs = append(pathArgs, petname.Generate(2, "-"))
		}

		projectSpecific, err := entityType.RequiresProject()
		s.Require().NoError(err)
		if !projectSpecific {
			// Unrestricted client has full access.
			cases = append(cases, testCase{
				id:           "unrestricted",
				entityURL:    entityURL,
				entitlements: entitlements,
			})

			if !slices.Contains([]entity.Type{entity.TypeServer, entity.TypeStoragePool, entity.TypeIdentity, entity.TypeProject, entity.TypeCertificate}, entityType) {
				// If it's not project specific and we don't have a special case, all access checks should be denied.
				cases = append(cases, testCase{
					id:            "foo-restricted",
					entityURL:     entityURL,
					entitlements:  entitlements,
					expectErr:     true,
					expectErrCode: http.StatusForbidden,
				})
			}

			continue
		}

		fooEntityURL, err := entityType.URL("foo", "", pathArgs...)
		s.Require().NoError(err)
		notFooEntityURL, err := entityType.URL(petname.Generate(2, "-"), "", pathArgs...)
		s.Require().NoError(err)

		// All checks against "foo" project should succeed. All checks in "not foo" should not succeed.
		cases = append(cases, testCase{
			id:           "foo-restricted",
			entityURL:    fooEntityURL,
			entitlements: entitlements,
		}, testCase{
			id:            "foo-restricted",
			entityURL:     notFooEntityURL,
			entitlements:  entitlements,
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		}, testCase{
			// Unrestricted client has full access.
			id:           "unrestricted",
			entityURL:    notFooEntityURL,
			entitlements: entitlements,
		})
	}

	for _, tt := range cases {
		entityType, _, _, _, err := entity.ParseURL(tt.entityURL.URL)
		s.Require().NoError(err)

		for _, entitlement := range tt.entitlements {
			details := request.RequestorArgs{
				Trusted:  true,
				Username: tt.id,
				Protocol: api.AuthenticationMethodTLS,
			}

			ctx := s.setupCtx(details)
			err := s.authorizer.CheckPermission(ctx, tt.entityURL, entitlement)
			if tt.expectErr {
				s.T().Logf("%q does not have %q on %q", tt.id, entitlement, tt.entityURL)
				s.Error(err)
				s.True(api.StatusErrorCheck(err, tt.expectErrCode))
			} else {
				s.T().Logf("%q has %q on %q", tt.id, entitlement, tt.entityURL)
				s.NoError(err)
			}

			// If we don't expect an error from CheckPermission (e.g. access is allowed), then we expect the permission
			// checker to return true (and vice versa).
			permissionChecker, err := s.authorizer.GetPermissionChecker(ctx, entitlement, entityType)
			s.NoError(err)
			s.Equal(!tt.expectErr, permissionChecker(tt.entityURL))
		}
	}
}
