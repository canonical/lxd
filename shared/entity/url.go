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

// EndpointEntityType derives the main entity type an endpoint is opertaing on from its URL.
// This is used to label endpoints for the API rates metrics.
func EndpointEntityType(url url.URL) Type {
	// Extract the url prefix without slashes and the API version if present.
	urlPrefix := strings.Split(strings.TrimPrefix(strings.TrimPrefix(url.Path, "/"+version.APIVersion), "/"), "/")[0]

	// Match the extracted prefix with recognized entity types' prefixes.
	for entityType, typeInfo := range entityTypes {
		if shared.ValueInSlice(urlPrefix, typeInfo.apiMetricsURLPrefixes()) {
			return entityType
		}
	}

	// Use TypeServer as the default, is used for undefined URLs.
	// This also applies to endpoints /, /{version}, /{version}/metrics, /{version}/events, /{version}/metadata and
	// /{version}/resources.
	return TypeServer
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

	// Set project parameter if provided and the entity type is not TypeProject (operations and warnings may be project
	// specific but it is not a requirement).
	if projectName != "" && t != TypeProject {
		u = u.WithQuery("project", projectName)
	}

	// Always set location if provided (empty or "none" locations are ignored).
	u = u.Target(location)
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

	requiresProject := entityTypeImpl.requiresProject()
	projectName = ""
	if requiresProject {
		projectName = u.Query().Get("project")
		if projectName == "" {
			projectName = api.ProjectDefaultName
		}
	}

	// If it's a project URL the project name is not a query parameter, it's in the path.
	if entityType == TypeProject {
		projectName = pathArguments[0]
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
