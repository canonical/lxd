package entity

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

func TestURL(t *testing.T) {
	tests := []struct {
		name               string
		rawURL             string
		expectedEntityType Type
		expectedProject    string
		expectedLocation   string
		expectedPathArgs   []string
		expectedErr        error
	}{
		{
			name:               "containers",
			rawURL:             "/1.0/containers/my-container?project=my-project",
			expectedEntityType: TypeContainer,
			expectedProject:    "my-project",
			expectedPathArgs:   []string{"my-container"},
			expectedErr:        nil,
		},
		{
			name:               "images",
			rawURL:             "/1.0/images/fwirnoaiwnerfoiawnef",
			expectedEntityType: TypeImage,
			expectedProject:    "default",
			expectedPathArgs:   []string{"fwirnoaiwnerfoiawnef"},
			expectedErr:        nil,
		},
		{
			name:               "profiles",
			rawURL:             "/1.0/profiles/my-profile?project=my-project",
			expectedEntityType: TypeProfile,
			expectedProject:    "my-project",
			expectedPathArgs:   []string{"my-profile"},
			expectedErr:        nil,
		},
		{
			name:               "projects",
			rawURL:             "/1.0/projects/my-project",
			expectedEntityType: TypeProject,
			expectedProject:    "",
			expectedPathArgs:   []string{"my-project"},
			expectedErr:        nil,
		},
		{
			name:               "certificates",
			rawURL:             "/1.0/certificates/foawienfoawnefkanwelfknsfl",
			expectedEntityType: TypeCertificate,
			expectedProject:    "",
			expectedPathArgs:   []string{"foawienfoawnefkanwelfknsfl"},
			expectedErr:        nil,
		},
		{
			name:               "instances",
			rawURL:             "/1.0/instances/my-instance",
			expectedEntityType: TypeInstance,
			expectedProject:    "default",
			expectedPathArgs:   []string{"my-instance"},
			expectedErr:        nil,
		},
		{
			name:               "instance backup",
			rawURL:             "/1.0/instances/my-instance/backups/my-backup?project=my-project",
			expectedEntityType: TypeInstanceBackup,
			expectedProject:    "my-project",
			expectedPathArgs:   []string{"my-instance", "my-backup"},
			expectedErr:        nil,
		},
		{
			name:               "instance snapshot",
			rawURL:             "/1.0/instances/my-instance/snapshots/my-snapshot",
			expectedEntityType: TypeInstanceSnapshot,
			expectedProject:    "default",
			expectedPathArgs:   []string{"my-instance", "my-snapshot"},
			expectedErr:        nil,
		},
		{
			name:               "networks",
			rawURL:             "/1.0/networks/my-network?project=my-project",
			expectedEntityType: TypeNetwork,
			expectedProject:    "my-project",
			expectedPathArgs:   []string{"my-network"},
			expectedErr:        nil,
		},
		{
			name:               "network acls",
			rawURL:             "/1.0/network-acls/my-network-acl",
			expectedEntityType: TypeNetworkACL,
			expectedProject:    "default",
			expectedPathArgs:   []string{"my-network-acl"},
			expectedErr:        nil,
		},
		{
			name:               "cluster members",
			rawURL:             "/1.0/cluster/members/node01",
			expectedEntityType: TypeNode,
			expectedProject:    "",
			expectedPathArgs:   []string{"node01"},
			expectedErr:        nil,
		},
		{
			name:               "operation",
			rawURL:             "/1.0/operations/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedEntityType: TypeOperation,
			expectedProject:    "",
			expectedPathArgs:   []string{"3e75d1bf-30ed-45ce-9e02-267fa7338eb4"},
			expectedErr:        nil,
		},
		{
			name:               "storage pools",
			rawURL:             "/1.0/storage-pools/my-storage-pool",
			expectedEntityType: TypeStoragePool,
			expectedProject:    "",
			expectedPathArgs:   []string{"my-storage-pool"},
			expectedErr:        nil,
		},
		{
			name:               "storage volumes",
			rawURL:             "/1.0/storage-pools/my-storage-pool/volumes/custom/my%2Fstorage-volume?project=my-project&target=node01",
			expectedEntityType: TypeStorageVolume,
			expectedProject:    "my-project",
			expectedLocation:   "node01",
			expectedPathArgs:   []string{"my-storage-pool", "custom", "my/storage-volume"},
			expectedErr:        nil,
		},
		{
			name:               "storage volume backups",
			rawURL:             "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/backups/my-backup?project=my-project",
			expectedEntityType: TypeStorageVolumeBackup,
			expectedProject:    "my-project",
			expectedPathArgs:   []string{"my-storage-pool", "custom", "my-storage-volume", "my-backup"},
			expectedErr:        nil,
		},
		{
			name:               "storage volume snapshots",
			rawURL:             "/1.0/storage-pools/my-storage-pool/volumes/custom/my-storage-volume/snapshots/my-snapshot?project=my-project",
			expectedEntityType: TypeStorageVolumeSnapshot,
			expectedProject:    "my-project",
			expectedPathArgs:   []string{"my-storage-pool", "custom", "my-storage-volume", "my-snapshot"},
			expectedErr:        nil,
		},
		{
			name:               "warnings",
			rawURL:             "/1.0/warnings/3e75d1bf-30ed-45ce-9e02-267fa7338eb4",
			expectedEntityType: TypeWarning,
			expectedProject:    "",
			expectedPathArgs:   []string{"3e75d1bf-30ed-45ce-9e02-267fa7338eb4"},
			expectedErr:        nil,
		},
		{
			name:               "cluster groups",
			rawURL:             "/1.0/cluster/groups/my-cluster-group",
			expectedEntityType: TypeClusterGroup,
			expectedProject:    "",
			expectedPathArgs:   []string{"my-cluster-group"},
			expectedErr:        nil,
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

			requiresProject, err := actualEntityType.RequiresProject()
			assert.NoError(t, err)
			if u.Query().Get("project") != "" || !requiresProject {
				// Assert that we can convert back to the same value.
				actualURL, err := actualEntityType.URL(actualProject, actualLocation, actualPathArgs...)
				assert.NoError(t, err)
				assert.Equal(t, tt.rawURL, actualURL.String())
			} else {
				// If the entity type requires a project but one wasn't set, assert that (entity.Type).URL sets the
				// default project.
				q := u.Query()
				q.Set("project", api.ProjectDefaultName)
				u.RawQuery = q.Encode()
				actualURL, err := actualEntityType.URL(actualProject, actualLocation, actualPathArgs...)
				assert.NoError(t, err)
				assert.Equal(t, u.String(), actualURL.String())
			}
		})
	}
}
