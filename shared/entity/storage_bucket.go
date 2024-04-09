package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeStorageBucket is an instantiated StorageBucket for convenience.
var TypeStorageBucket = StorageBucket{}

// TypeNameStorageBucket is the TypeName for StorageBucket entities.
const TypeNameStorageBucket TypeName = "storage_bucket"

// StorageBucket is an implementation of Type for StorageBucket entities.
type StorageBucket struct{}

// RequiresProject returns true for entity type StorageBucket.
func (t StorageBucket) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameStorageBucket.
func (t StorageBucket) Name() TypeName {
	return TypeNameStorageBucket
}

// PathTemplate returns the path template for entity type StorageBucket.
func (t StorageBucket) PathTemplate() []string {
	return []string{"storage-pools", pathPlaceholder, "buckets", pathPlaceholder}
}

// URL returns a URL for entity type StorageBucket.
func (t StorageBucket) URL(projectName string, location string, storagePoolName string, bucketName string) *api.URL {
	return urlMust(t, projectName, location, storagePoolName, bucketName)
}

// String implements fmt.Stringer for StorageBucket entities.
func (t StorageBucket) String() string {
	return string(t.Name())
}
