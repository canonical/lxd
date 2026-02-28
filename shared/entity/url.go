package entity

import (
	"fmt"
	"net/url"
	"runtime"
	"slices"
	"strings"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// nRequiredPathArguments returns the number of path arguments (mux variables) that are required to create a unique URL
// for the given typeInfo.
func nRequiredPathArguments(t typeInfo) int {
	nRequiredPathArguments := 0
	for _, element := range t.path() {
		if element == pathPlaceholder {
			nRequiredPathArguments++
		}
	}

	return nRequiredPathArguments
}

// URL returns a string URL for the Type.
//
// If the Type is project specific and no project name is given, the project name will be set to api.ProjectDefaultName.
//
// Warning: All arguments to this function will be URL encoded. They must not be URL encoded before calling this method.
func (t Type) URL(projectName string, location string, pathArguments ...string) (*api.URL, error) {
	info, ok := entityTypes[t]
	if !ok {
		return nil, fmt.Errorf("Invalid entity type %q", t)
	}

	if info.requiresProject() && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	nPathArgs := nRequiredPathArguments(info)
	if len(pathArguments) != nPathArgs {
		return nil, fmt.Errorf("Entity type %q requires `%d` path arguments but `%d` were given", t, nPathArgs, len(pathArguments))
	}

	pathParts := info.path()
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

	// Set project parameter if provided and the entity type requires a project.
	if projectName != "" && info.requiresProject() {
		u = u.WithQuery("project", projectName)
	}

	// Only set location if required to uniquely identify the entity.
	if info.requiresLocation() {
		u = u.Target(location)
	}

	return u, nil
}

// URLFromNamedArgs returns a string URL for the Type.
//
// If the Type is project specific and no project name is given, the project name will be set to api.ProjectDefaultName.
//
// Warning: All arguments to this function will be URL encoded. They must not be URL encoded before calling this method.
func (t Type) URLFromNamedArgs(projectName string, location string, pathArguments map[string]string) (*api.URL, error) {
	info, ok := entityTypes[t]
	if !ok {
		return nil, fmt.Errorf("Invalid entity type %q", t)
	}

	// Ensure only known path arguments are provided.
	argNames := info.pathArgNames()
	for name := range pathArguments {
		if !slices.Contains(argNames, name) {
			return nil, fmt.Errorf("Unknown path argument %q for entity type %q", name, t)
		}
	}

	// Convert the map of named path arguments to a slice of path arguments.
	args := make([]string, len(argNames))
	for i, name := range argNames {
		_, ok := pathArguments[name]
		if !ok {
			return nil, fmt.Errorf("Entity type %q requires path argument %q", t, name)
		}

		args[i] = pathArguments[name]
	}

	return t.URL(projectName, location, args...)
}

// ParseURL parses a raw URL string and returns the Type, project, location, and path arguments (mux vars).
//
// Path arguments are returned in the order they are found in the URL. If there is no project query parameter and the
// Type requires a project, then api.ProjectDefaultName is returned as the project name. The returned location is the
// value of the "target" query parameter. All returned values are unescaped.
func ParseURL(u url.URL) (entityType Type, projectName string, location string, pathArguments []string, err error) {
	path := u.Path
	if u.RawPath != "" {
		path = u.RawPath
	}

	pathSuffix, found := strings.CutPrefix(path, "/"+version.APIVersion+"/")
	if !found {
		if path == "/"+version.APIVersion {
			return TypeServer, "", "", nil, nil
		}

		return "", "", "", nil, fmt.Errorf("URL %q does not contain LXD API version", u.String())
	}

	pathParts := strings.Split(pathSuffix, "/")
	var entityTypeImpl typeInfo
entityTypeLoop:
	for t, info := range entityTypes {
		entityPath := info.path()

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
		entityType = t
		entityTypeImpl = info
		break
	}

	if entityType == "" {
		return "", "", "", nil, fmt.Errorf("Failed to match entity URL %q", u.String())
	}

	// Handle the project query parameter. If the entity type requires a project we set it to
	// [api.ProjectDefaultName] if it does not exist.
	requiresProject := entityTypeImpl.requiresProject()
	projectName = ""
	if requiresProject {
		projectName = u.Query().Get("project")
		if projectName == "" && requiresProject {
			projectName = api.ProjectDefaultName
		}
	}

	// If it's a project URL the project name is not a query parameter, it's in the path.
	if entityType == TypeProject {
		projectName = pathArguments[0]
	}

	return entityType, projectName, u.Query().Get("target"), pathArguments, nil
}

// ParseURLWithNamedArgs parses a raw URL string and returns the Type and a map of named arguments,
// where each entry maps an argument name to its corresponding value parsed from the URL.
func ParseURLWithNamedArgs(u url.URL) (entityType Type, projectName string, location string, args map[string]string, err error) {
	entityType, projectName, location, pathArgs, err := ParseURL(u)
	if err != nil {
		return "", "", "", nil, err
	}

	entityInfo, ok := entityTypes[entityType]
	if !ok {
		return "", "", "", nil, fmt.Errorf("Unknown entity type %q", entityType)
	}

	pathArgNames := entityInfo.pathArgNames()

	if len(pathArgs) != len(pathArgNames) {
		return "", "", "", nil, fmt.Errorf("Argument count mismatch for entity type %q: expected %d, got %d", entityType, len(pathArgNames), len(pathArgs))
	}

	args = make(map[string]string, len(pathArgs))
	for i, argName := range pathArgNames {
		args[argName] = pathArgs[i]
	}

	return entityType, projectName, location, args, nil
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

// InstanceBackupURL returns an *api.URL to an instance backup.
func InstanceBackupURL(projectName string, instanceName string, backupName string) *api.URL {
	return TypeInstanceBackup.urlMust(projectName, "", instanceName, backupName)
}

// InstanceSnapshotURL returns an *api.URL to an instance snapshot.
func InstanceSnapshotURL(projectName string, instanceName string, snapshotName string) *api.URL {
	return TypeInstanceSnapshot.urlMust(projectName, "", instanceName, snapshotName)
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

// StorageVolumeBackupURL returns an *api.URL to a storage volume backup.
func StorageVolumeBackupURL(projectName string, location string, poolName string, volumeTypeName string, volumeName, backupName string) *api.URL {
	return TypeStorageVolumeBackup.urlMust(projectName, location, poolName, volumeTypeName, volumeName, backupName)
}

// StorageVolumeSnapshotURL returns an *api.URL to a storage volume snapshot.
func StorageVolumeSnapshotURL(projectName string, location string, poolName string, volumeTypeName string, volumeName, backupName string) *api.URL {
	return TypeStorageVolumeSnapshot.urlMust(projectName, location, poolName, volumeTypeName, volumeName, backupName)
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

// PlacementGroupURL returns an [*api.URL] to a placement group.
func PlacementGroupURL(projectName string, placementGroupName string) *api.URL {
	return TypePlacementGroup.urlMust(projectName, "", placementGroupName)
}
