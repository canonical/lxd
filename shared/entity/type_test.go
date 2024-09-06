package entity

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

func TestURL(t *testing.T) {
	tests := []struct {
		name                  string
		rawURL                string
		expectedNormalisedURL string
		expectedEntityType    Type
		expectedProject       string
		expectedLocation      string
		expectedPathArgs      []string
		expectedErr           error
	}{
		{
			name:        "not a LXD URL",
			rawURL:      "/1.0/not/a/url",
			expectedErr: fmt.Errorf("Failed to match entity URL %q", "/1.0/not/a/url"),
		},
		{
			name:                  "containers",
			rawURL:                "/1.0/containers/my-container?project=my-project",
			expectedNormalisedURL: "/1.0/containers/my-container?project=my-project",
			expectedEntityType:    TypeContainer,
			expectedProject:       "my-project",
			expectedPathArgs:      []string{"my-container"},
			expectedErr:           nil,
		},
		{
			name:                  "images",
			rawURL:                "/1.0/images/fwirnoaiwnerfoiawnef",
			expectedNormalisedURL: "/1.0/images/fwirnoaiwnerfoiawnef?project=default",
			expectedEntityType:    TypeImage,
			expectedProject:       api.ProjectDefaultName,
			expectedPathArgs:      []string{"fwirnoaiwnerfoiawnef"},
			expectedErr:           nil,
		},
		{
			name:                  "profiles",
			rawURL:                "/1.0/profiles/my-profile?project=my-project",
			expectedNormalisedURL: "/1.0/profiles/my-profile?project=my-project",
			expectedEntityType:    TypeProfile,
			expectedProject:       "my-project",
			expectedPathArgs:      []string{"my-profile"},
			expectedErr:           nil,
		},
		{
			name:                  "projects",
			rawURL:                "/1.0/projects/my-project",
			expectedNormalisedURL: "/1.0/projects/my-project",
			expectedEntityType:    TypeProject,
			expectedProject:       "my-project",
			expectedPathArgs:      []string{"my-project"},
			expectedErr:           nil,
		},
		{
			name:                  "certificates",
			rawURL:                "/1.0/certificates/foawienfoawnefkanwelfknsfl",
			expectedNormalisedURL: "/1.0/certificates/foawienfoawnefkanwelfknsfl",
			expectedEntityType:    TypeCertificate,
			expectedProject:       "",
			expectedPathArgs:      []string{"foawienfoawnefkanwelfknsfl"},
			expectedErr:           nil,
		},
		{
			name:                  "instances",
			rawURL:                "/1.0/instances/my-instance",
			expectedNormalisedURL: "/1.0/instances/my-instance?project=default",
			expectedEntityType:    TypeInstance,
			expectedProject:       api.ProjectDefaultName,
			expectedPathArgs:      []string{"my-instance"},
			expectedErr:           nil,
		},
		{
			name:                  "instance backup",
			rawURL:                "/1.0/instances/my-instance/backups/my-backup?project=my-project",
			expectedNormalisedURL: "/1.0/instances/my-instance/backups/my-backup?project=my-project",
			expectedEntityType:    TypeInstanceBackup,
			expectedProject:       "my-project",
			expectedPathArgs:      []string{"my-instance", "my-backup"},
			expectedErr:           nil,
		},
		{
			name:                  "instance snapshot",
			rawURL:                "/1.0/instances/my-instance/snapshots/my-snapshot",
			expectedNormalisedURL: "/1.0/instances/my-instance/snapshots/my-snapshot?project=default",
			expectedEntityType:    TypeInstanceSnapshot,
			expectedProject:       api.ProjectDefaultName,
			expectedPathArgs:      []string{"my-instance", "my-snapshot"},
			expectedErr:           nil,
		},
		{
			name:                  "networks",
			rawURL:                "/1.0/networks/my-network?project=my-project",
			expectedNormalisedURL: "/1.0/networks/my-network?project=my-project",
			expectedEntityType:    TypeNetwork,
			expectedProject:       "my-project",
			expectedPathArgs:      []string{"my-network"},
			expectedErr:           nil,
		},
		{
			name:                  "network acls",
			rawURL:                "/1.0/network-acls/my-network-acl",
			expectedNormalisedURL: "/1.0/network-acls/my-network-acl?project=default",
			expectedEntityType:    TypeNetworkACL,
			expectedProject:       api.ProjectDefaultName,
			expectedPathArgs:      []string{"my-network-acl"},
			expectedErr:           nil,
		},
		{
			name:                  "cluster members",
			rawURL:                "/1.0/cluster/members/node01",
			expectedNormalisedURL: "/1.0/cluster/members/node01",
			expectedEntityType:    TypeClusterMember,
			expectedProject:       "",
			expectedPathArgs:      []string{"node01"},
			expectedErr:           nil,
		},
		{
			name:                  "operation",
			rawURL:                "/1.0/operations/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedNormalisedURL: "/1.0/operations/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedEntityType:    TypeOperation,
			expectedProject:       "",
			expectedPathArgs:      []string{"3e75d1bf-30ed-45ce-9e02-267fa7338eb4"},
			expectedErr:           nil,
		},
		{
			name:                  "storage pools",
			rawURL:                "/1.0/storage-pools/my-storage-pool",
			expectedNormalisedURL: "/1.0/storage-pools/my-storage-pool",
			expectedEntityType:    TypeStoragePool,
			expectedProject:       "",
			expectedPathArgs:      []string{"my-storage-pool"},
			expectedErr:           nil,
		},
		{
			name:                  "storage volumes",
			rawURL:                "/1.0/storage-pools/my-storage-pool/volumes/custom/my%2Fstorage-volume?project=my-project&target=node01",
			expectedNormalisedURL: "/1.0/storage-pools/my-storage-pool/volumes/custom/my%2Fstorage-volume?project=my-project&target=node01",
			expectedEntityType:    TypeStorageVolume,
			expectedProject:       "my-project",
			expectedLocation:      "node01",
			expectedPathArgs:      []string{"my-storage-pool", "custom", "my/storage-volume"},
			expectedErr:           nil,
		},
		{
			name:                  "storage volume backups",
			rawURL:                "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/backups/my-backup?project=my-project",
			expectedNormalisedURL: "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/backups/my-backup?project=my-project",
			expectedEntityType:    TypeStorageVolumeBackup,
			expectedProject:       "my-project",
			expectedPathArgs:      []string{"my-storage-pool", "custom", "my-storage-volume", "my-backup"},
			expectedErr:           nil,
		},
		{
			name:                  "storage volume snapshots",
			rawURL:                "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/snapshots/my-snapshot?project=my-project",
			expectedNormalisedURL: "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/snapshots/my-snapshot?project=my-project",
			expectedEntityType:    TypeStorageVolumeSnapshot,
			expectedProject:       "my-project",
			expectedPathArgs:      []string{"my-storage-pool", "custom", "my-storage-volume", "my-snapshot"},
			expectedErr:           nil,
		},
		{
			name:                  "storage buckets",
			rawURL:                "/1.0/storage-pools/my-storage-pool/buckets/my-bucket",
			expectedNormalisedURL: "/1.0/storage-pools/my-storage-pool/buckets/my-bucket?project=default",
			expectedEntityType:    TypeStorageBucket,
			expectedProject:       api.ProjectDefaultName,
			expectedPathArgs:      []string{"my-storage-pool", "my-bucket"},
			expectedErr:           nil,
		},
		{
			name:                  "warnings",
			rawURL:                "/1.0/warnings/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedNormalisedURL: "/1.0/warnings/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedEntityType:    TypeWarning,
			expectedProject:       "",
			expectedPathArgs:      []string{"3e75d1bf-30ed-45ce-9e02-267fa7338eb4"},
			expectedErr:           nil,
		},
		{
			name:                  "cluster groups",
			rawURL:                "/1.0/cluster/groups/my-cluster-group",
			expectedNormalisedURL: "/1.0/cluster/groups/my-cluster-group",
			expectedEntityType:    TypeClusterGroup,
			expectedProject:       "",
			expectedPathArgs:      []string{"my-cluster-group"},
			expectedErr:           nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			require.NoError(t, err)
			actualEntityType, actualProject, actualLocation, actualPathArgs, actualErr := ParseURL(*u)

			assert.Equal(t, tt.expectedEntityType, actualEntityType)
			assert.Equal(t, tt.expectedProject, actualProject)
			assert.Equal(t, tt.expectedLocation, actualLocation)
			for i, pathArg := range actualPathArgs {
				assert.Equal(t, tt.expectedPathArgs[i], pathArg)
			}

			assert.Equal(t, tt.expectedErr, actualErr)
			if tt.expectedErr != nil {
				return
			}

			normalisedURL, err := actualEntityType.URL(actualProject, actualLocation, actualPathArgs...)
			assert.Equal(t, normalisedURL.String(), tt.expectedNormalisedURL)
			assert.NoError(t, err)
		})
	}

	endpointTests := []struct {
		name               string
		rawURL             string
		expectedEntityType Type
	}{
		{
			name:               "not a LXD endpoint",
			rawURL:             "/1.0/not/a/url",
			expectedEntityType: TypeServer,
		},
		{
			name:               "containers endpoint",
			rawURL:             "/1.0/containers/my-container",
			expectedEntityType: TypeInstance,
		},
		{
			name:               "images endpoint",
			rawURL:             "/1.0/images/fwirnoaiwnerfoiawnef",
			expectedEntityType: TypeImage,
		},
		{
			name:               "profiles endpoint",
			rawURL:             "/1.0/profiles/my-profile",
			expectedEntityType: TypeProfile,
		},
		{
			name:               "projects endpoint",
			rawURL:             "/1.0/projects/my-project",
			expectedEntityType: TypeProject,
		},
		{
			name:               "certificates endpoint",
			rawURL:             "/1.0/certificates/foawienfoawnefkanwelfknsfl",
			expectedEntityType: TypeIdentity,
		},
		{
			name:               "instances endpoint",
			rawURL:             "/1.0/instances/my-instance",
			expectedEntityType: TypeInstance,
		},
		{
			name:               "instance backup endpoint",
			rawURL:             "/1.0/instances/my-instance/backups/my-backup",
			expectedEntityType: TypeInstance,
		},
		{
			name:               "instance snapshot endpoint",
			rawURL:             "/1.0/instances/my-instance/snapshots/my-snapshot",
			expectedEntityType: TypeInstance,
		},
		{
			name:               "networks endpoint",
			rawURL:             "/1.0/networks/my-network?project=my-project",
			expectedEntityType: TypeNetwork,
		},
		{
			name:               "network acls endpoint",
			rawURL:             "/1.0/network-acls/my-network-acl",
			expectedEntityType: TypeNetwork,
		},
		{
			name:               "cluster members endpoint",
			rawURL:             "/1.0/cluster/members/node01",
			expectedEntityType: TypeClusterMember,
		},
		{
			name:               "operation endpoint",
			rawURL:             "/1.0/operations/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedEntityType: TypeOperation,
		},
		{
			name:               "storage pools endpoint",
			rawURL:             "/1.0/storage-pools/my-storage-pool",
			expectedEntityType: TypeStoragePool,
		},
		{
			name:               "storage volumes endpoint",
			rawURL:             "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume",
			expectedEntityType: TypeStoragePool,
		},
		{
			name:               "storage volume backups endpoint",
			rawURL:             "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/backups",
			expectedEntityType: TypeStoragePool,
		},
		{
			name:               "storage volume snapshots endpoint",
			rawURL:             "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/snapshots",
			expectedEntityType: TypeStoragePool,
		},
		{
			name:               "list storage volumes endpoint",
			rawURL:             "/1.0/storage-volumes",
			expectedEntityType: TypeStoragePool,
		},
		{
			name:               "storage buckets endpoint",
			rawURL:             "/1.0/storage-pools/my-storage-pool/buckets/my-bucket",
			expectedEntityType: TypeStoragePool,
		},
		{
			name:               "warnings endpoint",
			rawURL:             "/1.0/warnings/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedEntityType: TypeWarning,
		},
		{
			name:               "cluster groups endpoint",
			rawURL:             "/1.0/cluster/groups/my-cluster-group",
			expectedEntityType: TypeClusterMember,
		},
	}

	apiMetricsEntityTypes := APIMetricsEntityTypes()
	for _, tt := range endpointTests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			require.NoError(t, err)
			actualEntityType := EndpointEntityType(*u)

			assert.Equal(t, tt.expectedEntityType, actualEntityType)
			assert.Contains(t, apiMetricsEntityTypes, actualEntityType)
		})
	}
}
