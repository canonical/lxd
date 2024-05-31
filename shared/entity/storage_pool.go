package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeStoragePool is an instantiated StoragePool for convenience.
var TypeStoragePool = StoragePool{}

// TypeNameStoragePool is the TypeName for StoragePool entities.
const TypeNameStoragePool TypeName = "storage_pool"

// StoragePool is an implementation of Type for StoragePool entities.
type StoragePool struct{}

// RequiresProject returns false for entity type StoragePool.
func (t StoragePool) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameStoragePool.
func (t StoragePool) Name() TypeName {
	return TypeNameStoragePool
}

// PathTemplate returns the path template for entity type StoragePool.
func (t StoragePool) PathTemplate() []string {
	return []string{"storage-pools", pathPlaceholder}
}

// URL returns a URL for entity type StoragePool.
func (t StoragePool) URL(storagePoolName string) *api.URL {
	return urlMust(t, "", "", storagePoolName)
}

// String implements fmt.Stringer for StoragePool entities.
func (t StoragePool) String() string {
	return string(t.Name())
}
