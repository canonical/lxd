package entity

import (
	"fmt"
	"net/url"
	"runtime"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// TypeName is a concrete type to be used as the Name of a Type.
type TypeName string

// Type is a representation of a LXD API resource. To add a new Type, implement this interface and add it to the list of
// types below. If the Type must be representable in the database, you must also add a new EntityType in the lxd/db/cluster
// package.
type Type interface {
	// Stringer - All Type implementations must implement fmt.Stringer. This should be the result of Name, cast to a string.
	fmt.Stringer

	// RequiresProject must return true if the entity type requires a project query parameter to be uniquely addressable
	// via the API.
	RequiresProject() bool

	// Name must return a unique TypeName for this entity type.
	Name() TypeName

	// PathTemplate must return a slice containing elements of the URL for this entity after version.APIVersion. The
	// pathPlaceholder must be used in place of path arguments (mux variables).
	PathTemplate() []string
}

const (
	// pathPlaceholder is used to indicate that a path argument is expected in a URL.
	pathPlaceholder = "{pathArgument}"
)

var entityTypes = []Type{
	TypeContainer,
	TypeImage,
	TypeProfile,
	TypeProject,
	TypeCertificate,
	TypeInstance,
	TypeInstanceBackup,
	TypeInstanceSnapshot,
	TypeNetwork,
	TypeNetworkACL,
	TypeNode,
	TypeOperation,
	TypeStoragePool,
	TypeStorageVolume,
	TypeStorageVolumeBackup,
	TypeStorageVolumeSnapshot,
	TypeWarning,
	TypeClusterGroup,
	TypeStorageBucket,
	TypeServer,
	TypeImageAlias,
	TypeNetworkZone,
	TypeIdentity,
	TypeAuthGroup,
	TypeIdentityProviderGroup,
}

// String implements fmt.Stringer for Type.
func (t Type) String() string {
	return string(t)
}

// Validate returns an error if the Type is not in the list of allowed types. If the allowEmpty argument is set to true
// an empty string is allowed. This is to accommodate that warnings may not refer to a specific entity type.
func (t Type) Validate() error {
	if !shared.ValueInSlice(t, entityTypes) {
		return fmt.Errorf("Unknown entity type %q", t)
	}

	return nil
}

// nRequiredPathArguments returns the number of path arguments (mux variables) that are required to create a unique URL
// for the given Type.
func (t Type) nRequiredPathArguments() (int, error) {
	err := t.Validate()
	if err != nil {
		return 0, err
	}

	typePath, err := t.path()
	if err != nil {
		return 0, err
	}

	nRequiredPathArguments := 0
	for _, element := range typePath {
		if element == pathPlaceholder {
			nRequiredPathArguments++
		}
	}

	return nRequiredPathArguments, nil
}

// URL returns a string URL for the Type.
//
// If the Type is project specific and no project name is given, the project name will be set to api.ProjectDefaultName.
//
// Warning: All arguments to this function will be URL encoded. They must not be URL encoded before calling this method.
func (t Type) URL(projectName string, location string, pathArguments ...string) (*api.URL, error) {
	requiresProject, err := t.RequiresProject()
	if err != nil {
		return nil, fmt.Errorf("Failed to check if entity type %q is project specific: %w", t, err)
	}

	if requiresProject && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	nRequiredPathArguments, err := t.nRequiredPathArguments()
	if err != nil {
		return nil, fmt.Errorf("Failed to check number of required path arguments for entity type %q: %w", t, err)
	}

	if len(pathArguments) != nRequiredPathArguments {
		return nil, fmt.Errorf("Entity type %q requires `%d` path arguments but `%d` were given", t, nRequiredPathArguments, len(pathArguments))
	}

	pathParts, err := t.path()
	if err != nil {
		return nil, fmt.Errorf("Failed to get path template for entity type %q: %w", t, err)
	}

	argIdx := 0
	path := []string{version.APIVersion}
	for _, pathPart := range pathParts {
		if pathPart == pathPlaceholder {
			pathPart = pathArguments[argIdx]
			argIdx++
		}

		path = append(path, pathPart)
	}

	u := api.NewURL().Path(path...)

	// Always set project parameter if provided (operations and warnings may be project specific but it is not a requirement).
	if projectName != "" {
		u = u.WithQuery("project", projectName)
	}

	// Always set location if provided.
	if location != "" {
		u = u.WithQuery("target", location)
	}

	return u, nil
}

// ParseURL parses a raw URL string and returns the Type, project, location, and path arguments (mux vars).
//
// Path arguments are returned in the order they are found in the URL. If there is no project query parameter and the
// Type requires a project, then api.ProjectDefaultName is returned as the project name. The returned location is the
// value of the "target" query parameter. All returned values are unescaped.
func ParseURL(u url.URL) (entityType Type, projectName string, location string, pathArguments []string, err error) {
	if u.Path == "/"+version.APIVersion {
		return TypeServer, "", "", nil, nil
	}

	path := u.Path
	if u.RawPath != "" {
		path = u.RawPath
	}

	if !strings.HasPrefix(path, "/"+version.APIVersion+"/") {
		return "", "", "", nil, fmt.Errorf("URL %q does not contain LXD API version", u.String())
	}

	pathParts := strings.Split(strings.TrimPrefix(path, "/"+version.APIVersion+"/"), "/")

entityTypeLoop:
	for _, currentEntityType := range entityTypes {
		entityPath, err := currentEntityType.path()
		if err != nil {
			return "", "", "", nil, fmt.Errorf("Failed to get path of entity type %q: %w", currentEntityType, err)
		}

		// Skip if we don't have the same number of slashes.
		if len(pathParts) != len(entityPath) {
			continue
		}

		var pathArgs []string
		for i, entityPathPart := range entityPath {
			if entityPathPart == pathPlaceholder {
				pathArgument, err := url.PathUnescape(pathParts[i])
				if err != nil {
					return "", "", "", nil, fmt.Errorf("Failed to unescape path element %q from url %q: %w", pathParts[i], u.String(), err)
				}

				pathArgs = append(pathArgs, pathArgument)
				continue
			}

			if entityPathPart != pathParts[i] {
				continue entityTypeLoop
			}
		}

		pathArguments = pathArgs
		entityType = currentEntityType
		break
	}

	if entityType == "" {
		return "", "", "", nil, fmt.Errorf("Failed to match entity URL %q", u.String())
	}

	requiresProject, _ := entityType.RequiresProject()
	projectName = ""
	if requiresProject {
		projectName = u.Query().Get("project")
		if projectName == "" {
			projectName = api.ProjectDefaultName
		}
	}

	return entityType, projectName, u.Query().Get("target"), pathArguments, nil
}

// urlMust is used internally when we know that creation of an *api.URL ought to succeed. If an error does occur an
// empty string is return and the error is logged with as much context as possible, including the file and line number
// of the caller.
func (t Type) urlMust(projectName string, location string, pathArguments ...string) *api.URL {
	ref, err := t.URL(projectName, location, pathArguments...)
	if err != nil {
		logCtx := logger.Ctx{"entity_type": t, "project_name": projectName, "location": location, "path_aguments": pathArguments}

		// Get the second caller (we expect the first caller to be internal to this package since this method is not exported).
		_, file, line, ok := runtime.Caller(2)
		if ok {
			logCtx["caller"] = fmt.Sprintf("%s#%d", file, line)
		}

		logger.Error("Failed to create entity URL", logCtx)
		return api.NewURL()
	}

	return ref
}

// ProjectURL returns an *api.URL to a Project.
func ProjectURL(projectName string) *api.URL {
	return TypeProject.urlMust("", "", projectName)
}

// InstanceURL returns an *api.URL to an instance.
func InstanceURL(projectName string, instanceName string) *api.URL {
	return TypeInstance.urlMust(projectName, "", instanceName)
}

// ServerURL returns an *api.URL to the server.
func ServerURL() *api.URL {
	return TypeServer.urlMust("", "")
}

// CertificateURL returns an *api.URL to a certificate.
func CertificateURL(fingerprint string) *api.URL {
	return TypeCertificate.urlMust("", "", fingerprint)
}

// ImageURL returns an *api.URL to an image.
func ImageURL(projectName string, imageName string) *api.URL {
	return TypeImage.urlMust(projectName, "", imageName)
}

// ImageAliasURL returns an *api.URL to an image alias.
func ImageAliasURL(projectName string, imageAliasName string) *api.URL {
	return TypeImageAlias.urlMust(projectName, "", imageAliasName)
}

// ProfileURL returns an *api.URL to a profile.
func ProfileURL(projectName string, profileName string) *api.URL {
	return TypeProfile.urlMust(projectName, "", profileName)
}

// NetworkURL returns an *api.URL to a network.
func NetworkURL(projectName string, networkName string) *api.URL {
	return TypeNetwork.urlMust(projectName, "", networkName)
}

// NetworkACLURL returns an *api.URL to a network ACL.
func NetworkACLURL(projectName string, networkACLName string) *api.URL {
	return TypeNetworkACL.urlMust(projectName, "", networkACLName)
}

// NetworkZoneURL returns an *api.URL to a network zone.
func NetworkZoneURL(projectName string, networkZoneName string) *api.URL {
	return TypeNetworkZone.urlMust(projectName, "", networkZoneName)
}

// StoragePoolURL returns an *api.URL to a storage pool.
func StoragePoolURL(storagePoolName string) *api.URL {
	return TypeStoragePool.urlMust("", "", storagePoolName)
}

// StorageVolumeURL returns an *api.URL to a storage volume.
func StorageVolumeURL(projectName string, location string, storagePoolName string, storageVolumeType string, storageVolumeName string) *api.URL {
	return TypeStorageVolume.urlMust(projectName, location, storagePoolName, storageVolumeType, storageVolumeName)
}

// StorageBucketURL returns an *api.URL to a storage bucket.
func StorageBucketURL(projectName string, location string, storagePoolName string, storageBucketName string) *api.URL {
	return TypeStorageBucket.urlMust(projectName, location, storagePoolName, storageBucketName)
}

// IdentityURL returns an *api.URL to an identity.
func IdentityURL(authenticationMethod string, identifier string) *api.URL {
	return TypeIdentity.urlMust("", "", authenticationMethod, identifier)
}

// AuthGroupURL returns an *api.URL to a group.
func AuthGroupURL(groupName string) *api.URL {
	return TypeAuthGroup.urlMust("", "", groupName)
}

// IdentityProviderGroupURL returns an *api.URL to an identity provider group.
func IdentityProviderGroupURL(identityProviderGroupName string) *api.URL {
	return TypeIdentityProviderGroup.urlMust("", "", identityProviderGroupName)
}
