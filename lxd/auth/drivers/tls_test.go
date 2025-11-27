package drivers

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/dustinkirkland/golang-petname"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

type tlsSuite struct {
	suite.Suite
	authorizer          auth.Authorizer
	idCache             *identity.Cache
	fooRestrictedClient identity.CacheEntry
	unrestrictedClient  identity.CacheEntry
}

func TestTLSSuite(t *testing.T) {
	suite.Run(t, new(tlsSuite))
}

func (s *tlsSuite) SetupSuite() {
	var err error
	s.idCache = &identity.Cache{}
	s.authorizer, err = LoadAuthorizer(context.Background(), DriverTLS, logger.Log, s.idCache)
	s.Require().NoError(err)
	s.fooRestrictedClient = s.newIdentity("foo-restricted", api.IdentityTypeCertificateClientRestricted, []string{"foo"})
	s.unrestrictedClient = s.newIdentity("unrestricted", api.IdentityTypeCertificateClientUnrestricted, nil)
	err = s.idCache.ReplaceAll([]identity.CacheEntry{s.fooRestrictedClient, s.unrestrictedClient}, nil)
	s.Require().NoError(err)
}

func (s *tlsSuite) newIdentity(name string, identityType string, projects []string) identity.CacheEntry {
	cert, _, err := shared.GenerateMemCert(true, shared.CertOptions{})
	s.Require().NoError(err)
	x509Cert, err := shared.ParseCert(cert)
	s.Require().NoError(err)
	certFingerprint := shared.CertFingerprint(x509Cert)
	return identity.CacheEntry{
		Identifier:           certFingerprint,
		Name:                 name,
		AuthenticationMethod: api.AuthenticationMethodTLS,
		IdentityType:         identityType,
		Projects:             projects,
		Certificate:          x509Cert,
	}
}

func (s *tlsSuite) setupCtx(id *identity.CacheEntry) context.Context {
	var details request.RequestorArgs
	if id != nil {
		details.Username = id.Identifier
		details.Protocol = id.AuthenticationMethod
		details.Trusted = true
	}

	r := &http.Request{
		RemoteAddr: "127.0.0.1:53423",
	}

	err := request.SetRequestor(r, s.idCache, details)
	s.Require().NoError(err)
	return r.Context()
}

func (s *tlsSuite) TestTLSAuthorizer() {
	type testCase struct {
		id            *identity.CacheEntry
		entityURL     *api.URL
		entitlements  []auth.Entitlement
		expectErr     bool
		expectErrCode int
	}

	// Initial cases represent exceptions to entity types that are not project specific (e.g. cases handled by `allowProjectUnspecificEntityType`).
	cases := []testCase{
		{
			id:        &s.fooRestrictedClient,
			entityURL: entity.ServerURL(),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanViewResources,
				auth.EntitlementCanViewMetrics,
			},
		},
		{
			id:        &s.fooRestrictedClient,
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
			id:           &s.fooRestrictedClient,
			entityURL:    entity.IdentityURL(api.AuthenticationMethodTLS, s.fooRestrictedClient.Identifier),
			entitlements: []auth.Entitlement{auth.EntitlementCanView},
		},
		{
			id:        &s.fooRestrictedClient,
			entityURL: entity.IdentityURL(api.AuthenticationMethodTLS, s.fooRestrictedClient.Identifier),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:        &s.fooRestrictedClient,
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
			id:           &s.fooRestrictedClient,
			entityURL:    entity.CertificateURL(s.fooRestrictedClient.Identifier),
			entitlements: []auth.Entitlement{auth.EntitlementCanView},
		},
		{
			id:        &s.fooRestrictedClient,
			entityURL: entity.CertificateURL(s.fooRestrictedClient.Identifier),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:        &s.fooRestrictedClient,
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
			id:        &s.fooRestrictedClient,
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
			id:        &s.fooRestrictedClient,
			entityURL: entity.ProjectURL("foo"),
			entitlements: []auth.Entitlement{
				auth.EntitlementCanEdit,
				auth.EntitlementCanDelete,
			},
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		},
		{
			id:        &s.fooRestrictedClient,
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
				id:           &s.unrestrictedClient,
				entityURL:    entityURL,
				entitlements: entitlements,
			})

			if !slices.Contains([]entity.Type{entity.TypeServer, entity.TypeStoragePool, entity.TypeIdentity, entity.TypeProject, entity.TypeCertificate}, entityType) {
				// If it's not project specific and we don't have a special case, all access checks should be denied.
				cases = append(cases, testCase{
					id:            &s.fooRestrictedClient,
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
			id:           &s.fooRestrictedClient,
			entityURL:    fooEntityURL,
			entitlements: entitlements,
		}, testCase{
			id:            &s.fooRestrictedClient,
			entityURL:     notFooEntityURL,
			entitlements:  entitlements,
			expectErr:     true,
			expectErrCode: http.StatusForbidden,
		}, testCase{
			// Unrestricted client has full access.
			id:           &s.unrestrictedClient,
			entityURL:    notFooEntityURL,
			entitlements: entitlements,
		})
	}

	for _, tt := range cases {
		entityType, _, _, _, err := entity.ParseURL(tt.entityURL.URL)
		s.Require().NoError(err)

		for _, entitlement := range tt.entitlements {
			ctx := s.setupCtx(tt.id)
			err := s.authorizer.CheckPermission(ctx, tt.entityURL, entitlement)
			if tt.expectErr {
				s.T().Logf("%q does not have %q on %q", tt.id.Name, entitlement, tt.entityURL)
				s.Error(err)
				s.True(api.StatusErrorCheck(err, tt.expectErrCode))
			} else {
				s.T().Logf("%q has %q on %q", tt.id.Name, entitlement, tt.entityURL)
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
