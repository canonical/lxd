package entity

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

func TestReferenceFromURL(t *testing.T) {
	tests := []struct {
		name                 string
		rawURL               string
		expectedCanonicalURL api.URL
		expectedEntityType   Type
		expectedProject      string
		expectedName         string
		expectedLocation     string
		expectedPathArgs     []string
		expectedErr          error
	}{
		{
			name:        "not a LXD URL",
			rawURL:      "/1.0/not/a/url",
			expectedErr: fmt.Errorf("Failed to match entity URL %q", "/1.0/not/a/url"),
		},
		{
			name:                 "instances",
			rawURL:               "/1.0/instances/my-instance?project=proj",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "instances", "my-instance").WithQuery("project", "proj"),
			expectedEntityType:   TypeInstance,
			expectedProject:      "proj",
			expectedPathArgs:     []string{"my-instance"},
			expectedName:         "my-instance",
			expectedErr:          nil,
		},
		{
			name:                 "profiles",
			rawURL:               "/1.0/profiles/my-profile?project=proj",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "profiles", "my-profile").WithQuery("project", "proj"),
			expectedEntityType:   TypeProfile,
			expectedProject:      "proj",
			expectedPathArgs:     []string{"my-profile"},
			expectedName:         "my-profile",
			expectedErr:          nil,
		},
		{
			name:                 "images",
			rawURL:               "/1.0/images/my-image?project=default",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "images", "my-image").WithQuery("project", "default"),
			expectedEntityType:   TypeImage,
			expectedProject:      api.ProjectDefaultName,
			expectedPathArgs:     []string{"my-image"},
			expectedName:         "my-image",
			expectedErr:          nil,
		},
		{
			name:                 "networks",
			rawURL:               "/1.0/networks/my-net?project=netproj",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "networks", "my-net").WithQuery("project", "netproj"),
			expectedEntityType:   TypeNetwork,
			expectedProject:      "netproj",
			expectedPathArgs:     []string{"my-net"},
			expectedName:         "my-net",
			expectedErr:          nil,
		},
		{
			name:                 "network acls",
			rawURL:               "/1.0/network-acls/my-acl?project=aclproj",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "network-acls", "my-acl").WithQuery("project", "aclproj"),
			expectedEntityType:   TypeNetworkACL,
			expectedProject:      "aclproj",
			expectedPathArgs:     []string{"my-acl"},
			expectedName:         "my-acl",
			expectedErr:          nil,
		},
		{
			name:                 "network zones",
			rawURL:               "/1.0/network-zones/my-zone?project=zoneproj",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "network-zones", "my-zone").WithQuery("project", "zoneproj"),
			expectedEntityType:   TypeNetworkZone,
			expectedProject:      "zoneproj",
			expectedPathArgs:     []string{"my-zone"},
			expectedName:         "my-zone",
			expectedErr:          nil,
		},
		{
			name:                 "storage volumes",
			rawURL:               "/1.0/storage-pools/pool1/volumes/custom/vol1?target=node1&project=storproj",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "storage-pools", "pool1", "volumes", "custom", "vol1").WithQuery("target", "node1").WithQuery("project", "storproj"),
			expectedEntityType:   TypeStorageVolume,
			expectedProject:      "storproj",
			expectedLocation:     "node1",
			expectedPathArgs:     []string{"pool1", "custom", "vol1"},
			expectedName:         "vol1",
			expectedErr:          nil,
		},
		{
			name:                 "storage buckets",
			rawURL:               "/1.0/storage-pools/pool1/buckets/buck1?target=node2&project=buckproj",
			expectedCanonicalURL: *api.NewURL().Path("1.0", "storage-pools", "pool1", "buckets", "buck1").WithQuery("project", "buckproj"),
			expectedEntityType:   TypeStorageBucket,
			expectedProject:      "buckproj",
			expectedLocation:     "node2",
			expectedPathArgs:     []string{"pool1", "buck1"},
			expectedName:         "buck1",
			expectedErr:          nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			require.NoError(t, err)

			ref, err := ReferenceFromURL(*u)

			assert.Equal(t, tt.expectedErr, err)
			if tt.expectedErr != nil {
				return
			}

			assert.Equal(t, tt.expectedEntityType, ref.EntityType)
			assert.Equal(t, tt.expectedProject, ref.ProjectName)
			assert.Equal(t, tt.expectedLocation, ref.Location)
			assert.Equal(t, tt.expectedName, ref.Name())
			assert.Equal(t, tt.expectedCanonicalURL, *ref.URL())
			for i, pathArg := range ref.PathArgs {
				assert.Equal(t, tt.expectedPathArgs[i], pathArg)
			}
		})
	}
}

func TestNewReference(t *testing.T) {
	tests := []struct {
		name                 string
		project              string
		entityType           Type
		location             string
		pathArgs             []string
		expectedCanonicalURL api.URL
		expectedEntityType   Type
		expectedProject      string
		expectedLocation     string
		expectedPathArgs     []string
		expectedName         string
		expectedErr          error
	}{
		{
			name:        "missing entity type",
			project:     "proj",
			entityType:  "",
			expectedErr: errors.New("Missing entity type"),
		},
		{
			name:        "not a LXD entity type",
			project:     "proj",
			entityType:  Type("foobar"),
			pathArgs:    []string{"my-foo"},
			expectedErr: fmt.Errorf("Invalid entity type %q", "foobar"),
		},
		{
			name:                 "instance",
			project:              "proj",
			entityType:           TypeInstance,
			pathArgs:             []string{"my-instance"},
			expectedCanonicalURL: *api.NewURL().Path("1.0", "instances", "my-instance").WithQuery("project", "proj"),
			expectedEntityType:   TypeInstance,
			expectedProject:      "proj",
			expectedPathArgs:     []string{"my-instance"},
			expectedName:         "my-instance",
			expectedErr:          nil,
		},
		{
			name:                 "storage volume",
			project:              "storproj",
			entityType:           TypeStorageVolume,
			location:             "node1",
			pathArgs:             []string{"pool1", "custom", "vol1"},
			expectedCanonicalURL: *api.NewURL().Path("1.0", "storage-pools", "pool1", "volumes", "custom", "vol1").WithQuery("target", "node1").WithQuery("project", "storproj"),
			expectedEntityType:   TypeStorageVolume,
			expectedProject:      "storproj",
			expectedLocation:     "node1",
			expectedPathArgs:     []string{"pool1", "custom", "vol1"},
			expectedName:         "vol1",
			expectedErr:          nil,
		},
		{
			name:                 "server",
			project:              "",
			entityType:           TypeServer,
			pathArgs:             nil,
			expectedCanonicalURL: *api.NewURL().Path("1.0"),
			expectedEntityType:   TypeServer,
			expectedProject:      "",
			expectedLocation:     "",
			expectedPathArgs:     nil,
			expectedName:         "server",
			expectedErr:          nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := NewReference(tt.project, tt.entityType, tt.location, tt.pathArgs...)

			assert.Equal(t, tt.expectedErr, err)
			if tt.expectedErr != nil {
				return
			}

			require.NotNil(t, ref)
			assert.Equal(t, tt.expectedEntityType, ref.EntityType)
			assert.Equal(t, tt.expectedProject, ref.ProjectName)
			assert.Equal(t, tt.expectedLocation, ref.Location)
			assert.Equal(t, tt.expectedName, ref.Name())
			assert.Equal(t, tt.expectedCanonicalURL, *ref.URL())
			for i, pathArg := range ref.PathArgs {
				assert.Equal(t, tt.expectedPathArgs[i], pathArg)
			}
		})
	}
}

func TestGetPathArgs(t *testing.T) {
	tests := []struct {
		name     string
		pathArgs []string
		numParts int
		expected []string
	}{
		{name: "negative parts", pathArgs: []string{"a", "b"}, numParts: -1, expected: nil},
		{name: "too many parts", pathArgs: []string{"a", "b"}, numParts: 3, expected: nil},
		{name: "one part", pathArgs: []string{"a", "b"}, numParts: 1, expected: []string{"a"}},
		{name: "two parts", pathArgs: []string{"a", "b"}, numParts: 2, expected: []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := Reference{EntityType: TypeInstance, ProjectName: "p", Location: "", PathArgs: tt.pathArgs}
			assert.Equal(t, tt.expected, ref.GetPathArgs(tt.numParts))
		})
	}
}
