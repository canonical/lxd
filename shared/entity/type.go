package entity

import (
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"

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

// types is a list of all Type implementations. To add a new entity type, create a new type that implements Type,
// instantiate it, and add it to this list.
var types = []Type{
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
	TypeClusterMember,
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

// nameToType is an internal map of TypeName to Type. It is populated on init from the types list.
var nameToType = make(map[TypeName]Type, len(types))

func init() {
	for _, t := range types {
		nameToType[t.Name()] = t
	}
}

// TypeFromString returns the Type with the given name, or an error if there is no Type with said name.
func TypeFromString(name string) (Type, error) {
	t, ok := nameToType[TypeName(name)]
	if !ok {
		return nil, api.StatusErrorf(http.StatusNotFound, "Entity type %q not found", name)
	}

	return t, nil
}

// nRequiredPathArguments returns the number of path arguments (mux variables) that are required to create a unique URL
// for the given Type.
func nRequiredPathArguments(t Type) int {
	nRequiredPathArguments := 0
	for _, element := range t.PathTemplate() {
		if element == pathPlaceholder {
			nRequiredPathArguments++
		}
	}

	return nRequiredPathArguments
}

// Equal returns true if the expected Type is equal to the expected Type.
func Equal(expected Type, actual Type) bool {
	return expected.Name() == actual.Name()
}

// URL returns a string URL for the Type.
//
// If the Type is project specific and no project name is given, the project name will be set to api.ProjectDefaultName.
//
// Warning: All arguments to this function will be URL encoded. They must not be URL encoded before calling this method.
func URL(t Type, projectName string, location string, pathArguments ...string) (*api.URL, error) {
	requiresProject := t.RequiresProject()

	if requiresProject && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	nRequiredPathArguments := nRequiredPathArguments(t)

	if len(pathArguments) != nRequiredPathArguments {
		return nil, fmt.Errorf("Entity type %q requires `%d` path arguments but `%d` were given", t.Name(), nRequiredPathArguments, len(pathArguments))
	}

	argIdx := 0
	path := []string{version.APIVersion}
	for _, pathPart := range t.PathTemplate() {
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
		return nil, "", "", nil, fmt.Errorf("URL %q does not contain LXD API version", u.String())
	}

	pathParts := strings.Split(strings.TrimPrefix(path, "/"+version.APIVersion+"/"), "/")

entityTypeLoop:
	for _, currentEntityType := range types {
		entityPath := currentEntityType.PathTemplate()

		// Skip if we don't have the same number of slashes.
		if len(pathParts) != len(entityPath) {
			continue
		}

		var pathArgs []string
		for i, entityPathPart := range entityPath {
			if entityPathPart == pathPlaceholder {
				pathArgument, err := url.PathUnescape(pathParts[i])
				if err != nil {
					return nil, "", "", nil, fmt.Errorf("Failed to unescape path element %q from url %q: %w", pathParts[i], u.String(), err)
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

	if entityType == nil {
		return nil, "", "", nil, fmt.Errorf("Failed to match entity URL %q", u.String())
	}

	requiresProject := entityType.RequiresProject()
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
func urlMust(t Type, projectName string, location string, pathArguments ...string) *api.URL {
	ref, err := URL(t, projectName, location, pathArguments...)
	if err != nil {
		logCtx := logger.Ctx{"entity_type": t.Name(), "project_name": projectName, "location": location, "path_aguments": pathArguments}

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
