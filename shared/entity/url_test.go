package entity

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This test parses the given URL, and construct a new one from the result, where the final/resulting
// URL must match the original one.
//
// The test will fail if any entity type present in [entityTypes] is not covered by the test cases.
func TestEntityPermissionURL_RoundTrip(t *testing.T) {
	tests := []struct {
		Name     string
		URL      string
		WantType Type
		WantArgs map[string]string
	}{
		{
			Name:     "Auth group",
			URL:      "/1.0/auth/groups/test",
			WantType: TypeAuthGroup,
			WantArgs: map[string]string{
				"name": "test",
			},
		},

		{
			Name:     "Certificate",
			URL:      "/1.0/certificates/abc",
			WantType: TypeCertificate,
			WantArgs: map[string]string{
				"name": "abc",
			},
		},
		{
			Name:     "Cluster group",
			URL:      "/1.0/cluster/groups/foo",
			WantType: TypeClusterGroup,
			WantArgs: map[string]string{
				"name": "foo",
			},
		},
		{
			Name:     "Cluster member",
			URL:      "/1.0/cluster/members/foo",
			WantType: TypeClusterMember,
			WantArgs: map[string]string{
				"name": "foo",
			},
		},
		{
			Name:     "Container",
			URL:      "/1.0/containers/foo",
			WantType: TypeContainer,
			WantArgs: map[string]string{
				"name": "foo",
			},
		},
		{
			Name:     "Identity",
			URL:      "/1.0/auth/identities/oidc/foo",
			WantType: TypeIdentity,
			WantArgs: map[string]string{
				"authentication_method": "oidc",
				"name":                  "foo",
			},
		},
		{
			Name:     "Identity provider group",
			URL:      "/1.0/auth/identity-provider-groups/test",
			WantType: TypeIdentityProviderGroup,
			WantArgs: map[string]string{
				"name": "test",
			},
		},
		{
			Name:     "Image",
			URL:      "/1.0/images/000000000000",
			WantType: TypeImage,
			WantArgs: map[string]string{
				"name": "000000000000",
			},
		},
		{
			Name:     "Image alias",
			URL:      "/1.0/images/aliases/ubuntu",
			WantType: TypeImageAlias,
			WantArgs: map[string]string{
				"name": "ubuntu",
			},
		},
		{
			Name:     "Instance",
			URL:      "/1.0/instances/c1",
			WantType: TypeInstance,
			WantArgs: map[string]string{
				"name": "c1",
			},
		},
		{
			Name:     "Instance with project",
			URL:      "/1.0/instances/c1?project=foo",
			WantType: TypeInstance,
			WantArgs: map[string]string{
				"name": "c1",
			},
		},
		{
			Name:     "Instance backup",
			URL:      "/1.0/instances/myvm/backups/mybackup",
			WantType: TypeInstanceBackup,
			WantArgs: map[string]string{
				"instance": "myvm",
				"name":     "mybackup",
			},
		},
		{
			Name:     "Instance snapshot",
			URL:      "/1.0/instances/my-vm/snapshots/snap-0",
			WantType: TypeInstanceSnapshot,
			WantArgs: map[string]string{
				"instance": "my-vm",
				"name":     "snap-0",
			},
		},
		{
			Name:     "Network",
			URL:      "/1.0/networks/lxdbr0",
			WantType: TypeNetwork,
			WantArgs: map[string]string{
				"name": "lxdbr0",
			},
		},
		{
			Name:     "Network ACL",
			URL:      "/1.0/network-acls/1.2.3.4",
			WantType: TypeNetworkACL,
			WantArgs: map[string]string{
				"name": "1.2.3.4",
			},
		},
		{
			Name:     "Network zone",
			URL:      "/1.0/network-zones/1.2.3.4",
			WantType: TypeNetworkZone,
			WantArgs: map[string]string{
				"name": "1.2.3.4",
			},
		},
		{
			Name:     "Operation",
			URL:      "/1.0/operations/123",
			WantType: TypeOperation,
			WantArgs: map[string]string{
				"id": "123",
			},
		},
		{
			Name:     "Placement group",
			URL:      "/1.0/placement-groups/default",
			WantType: TypePlacementGroup,
			WantArgs: map[string]string{
				"name": "default",
			},
		},
		{
			Name:     "Profile",
			URL:      "/1.0/profiles/default",
			WantType: TypeProfile,
			WantArgs: map[string]string{
				"name": "default",
			},
		},
		{
			Name:     "Project",
			URL:      "/1.0/projects/foo",
			WantType: TypeProject,
			WantArgs: map[string]string{
				"name": "foo",
			},
		},
		{
			Name:     "Server",
			URL:      "/1.0",
			WantType: TypeServer,
			WantArgs: map[string]string{},
		},
		{
			Name:     "Storage pool",
			URL:      "/1.0/storage-pools/p1",
			WantType: TypeStoragePool,
			WantArgs: map[string]string{
				"name": "p1",
			},
		},
		{
			Name:     "Storage volume",
			URL:      "/1.0/storage-pools/p1/volumes/custom/v1",
			WantType: TypeStorageVolume,
			WantArgs: map[string]string{
				"pool": "p1",
				"type": "custom",
				"name": "v1",
			},
		},
		{
			Name:     "Storage volume backup",
			URL:      "/1.0/storage-pools/p1/volumes/custom/v1/backups/b1",
			WantType: TypeStorageVolumeBackup,
			WantArgs: map[string]string{
				"pool":   "p1",
				"type":   "custom",
				"volume": "v1",
				"name":   "b1",
			},
		},
		{
			Name:     "Storage volume snapshot",
			URL:      "/1.0/storage-pools/p1/volumes/custom/v1/snapshots/s1",
			WantType: TypeStorageVolumeSnapshot,
			WantArgs: map[string]string{
				"pool":   "p1",
				"type":   "custom",
				"volume": "v1",
				"name":   "s1",
			},
		},
		{
			Name:     "Storage bucket",
			URL:      "/1.0/storage-pools/test/buckets/pail",
			WantType: TypeStorageBucket,
			WantArgs: map[string]string{
				"pool": "test",
				"name": "pail",
			},
		},
		{
			Name:     "Storage volume with project and location",
			URL:      "/1.0/storage-pools/p1/volumes/custom/v1?project=foo&target=bar",
			WantType: TypeStorageVolume,
			WantArgs: map[string]string{
				"pool": "p1",
				"type": "custom",
				"name": "v1",
			},
		},
		{
			Name:     "Warning",
			URL:      "/1.0/warnings/123123",
			WantType: TypeWarning,
			WantArgs: map[string]string{
				"id": "123123",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			u, err := url.Parse(test.URL)
			require.NoError(t, err)

			entityType, project, location, args, err := ParseURLWithNamedArgs(*u)
			require.NoError(t, err)
			require.Equal(t, test.WantType, entityType)

			// Check that the parsed type and arguments match the expected values.
			argMap := map[string]string{}
			for _, arg := range args {
				argMap[arg.Name] = arg.Value
			}

			require.Equal(t, test.WantArgs, argMap)

			// Construct a new URL from the parsed type and arguments.
			pathArgs := make([]string, len(args))
			for i, arg := range args {
				pathArgs[i] = arg.Value
			}

			url, err := entityType.URL(project, location, pathArgs...)
			require.NoError(t, err)

			// If project is default, check if resulting URL has a query parameter project set.
			// In such case, validate the value of query parameter is default and then remove it
			// to simplify the comparison with the original URL.
			if project == "default" {
				q := url.Query()
				if q.Has("project") {
					require.Equal(t, "default", q.Get("project"))
					q.Del("project")
					url.RawQuery = q.Encode()
				}
			}

			require.Equal(t, test.URL, url.String())
		})
	}

	// Make sure that ALL entity types are covered by the test cases.
	// Run after tests to allow seeing the result of existing test cases.
	missingEntityTypes := []string{}
	for entityType := range entityTypes {
		found := false
		for _, test := range tests {
			if test.WantType == entityType {
				found = true
				break
			}
		}

		if !found {
			entityTypeStr := string(entityType)
			missingEntityTypes = append(missingEntityTypes, entityTypeStr)
		}
	}

	if len(missingEntityTypes) > 0 {
		slices.Sort(missingEntityTypes)
		t.Fatalf("Missing test cases for entity types:\n - %v", strings.Join(missingEntityTypes, "\n - "))
	}
}
