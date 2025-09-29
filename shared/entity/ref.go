package entity

import (
	"errors"
	"net/url"
	"slices"

	"github.com/canonical/lxd/shared/api"
)

// Reference represents a canonical entity reference.
type Reference struct {
	EntityType  Type
	ProjectName string
	Location    string
	PathArgs    []string
	url         api.URL
}

// NewReference constructs a [*Reference] and validates it by attempting to build the canonical URL.
func NewReference(projectName string, entityType Type, location string, pathArgs ...string) (*Reference, error) {
	if entityType == "" {
		return nil, errors.New("Missing entity type")
	}

	url, err := entityType.URL(projectName, location, pathArgs...)
	if err != nil {
		return nil, err
	}

	return &Reference{EntityType: entityType, ProjectName: projectName, Location: location, PathArgs: slices.Clone(pathArgs), url: *url}, nil
}

// ReferenceFromURL parses a [url.URL] into a [*Reference].
func ReferenceFromURL(u url.URL) (*Reference, error) {
	entityType, projectName, location, pathArgs, err := ParseURL(u)
	if err != nil {
		return nil, err
	}

	return NewReference(projectName, entityType, location, pathArgs...)
}

// URL returns an [*api.URL] for this [Reference].
func (r Reference) URL() *api.URL {
	return &r.url
}

// Name returns the name of the entity, which is the last path argument.
func (r Reference) Name() string {
	// Only the server entity type does not have any path arguments.
	if r.EntityType == TypeServer {
		return "server"
	}

	// The last path argument is always the entity name because URLs go from least specific to most specific.
	// For example:
	//   - /1.0/instances/{name}
	//   - /1.0/projects/{name}
	//   - /1.0/storage-pools/{pool}/volumes/{type}/{name}
	//   - /1.0/storage-pools/{pool}/volumes/{type}/{volume}/snapshots/{name}
	//   - /1.0/storage-pools/{pool}/buckets/{name}
	return r.PathArgs[len(r.PathArgs)-1]
}

// GetPathArgs returns the specified number of path parts, if available.
func (r Reference) GetPathArgs(numParts int) []string {
	if numParts < 0 {
		return nil
	}

	if len(r.PathArgs) < numParts {
		return nil
	}

	return r.PathArgs[:numParts]
}
