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
	url         *api.URL
}

// NewReference constructs a [Reference] and validates it by attempting to build the canonical URL.
func NewReference(projectName string, entityType Type, location string, pathArgs ...string) (Reference, error) {
	if entityType == "" {
		return Reference{}, errors.New("Missing entity type")
	}

	url, err := entityType.URL(projectName, location, pathArgs...)
	if err != nil {
		return Reference{}, err
	}

	return Reference{EntityType: entityType, ProjectName: projectName, Location: location, PathArgs: slices.Clone(pathArgs), url: url}, nil
}

// ReferenceFromURL parses a [url.URL] into a [Reference].
func ReferenceFromURL(u url.URL) (Reference, error) {
	entityType, projectName, location, pathArgs, err := ParseURL(u)
	if err != nil {
		return Reference{}, err
	}

	return NewReference(projectName, entityType, location, pathArgs...)
}

// URL returns an [*api.URL] for this [Reference].
func (r Reference) URL() *api.URL {
	return r.url
}

// Name returns the primary name identifier for single-name entities.
func (r Reference) Name() (string, bool) {
	switch r.EntityType {
	case TypeInstance, TypeProfile, TypeNetwork, TypeNetworkACL, TypeNetworkZone, TypeImage, TypeImageAlias, TypeCertificate:
		if len(r.PathArgs) >= 1 {
			return r.PathArgs[0], true
		}
	}

	return "", false
}

// StorageVolumeParts returns pool name, volume type, and volume name for storage volumes.
func (r Reference) StorageVolumeParts() (poolName string, volType string, volName string, ok bool) {
	if r.EntityType != TypeStorageVolume {
		return "", "", "", false
	}

	if len(r.PathArgs) < 3 {
		return "", "", "", false
	}

	return r.PathArgs[0], r.PathArgs[1], r.PathArgs[2], true
}

// StorageBucketParts returns pool name and bucket name for storage buckets.
func (r Reference) StorageBucketParts() (poolName string, bucketName string, ok bool) {
	if r.EntityType != TypeStorageBucket {
		return "", "", false
	}

	if len(r.PathArgs) < 2 {
		return "", "", false
	}

	return r.PathArgs[0], r.PathArgs[1], true
}
