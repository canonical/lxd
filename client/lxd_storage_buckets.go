package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetStoragePoolBucketNames returns a list of storage bucket names.
func (r *ProtocolLXD) GetStoragePoolBucketNames(poolName string) ([]string, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("storage-pools", poolName, "buckets")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetStoragePoolBuckets returns a list of storage buckets for the provided pool.
func (r *ProtocolLXD) GetStoragePoolBuckets(poolName string) ([]api.StorageBucket, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	buckets := []api.StorageBucket{}

	// Fetch the raw value.
	u := api.NewURL().Path("storage-pools", poolName, "buckets").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &buckets)
	if err != nil {
		return nil, err
	}

	return buckets, nil
}

// GetStoragePoolBucketsAllProjects returns a list of storage pool buckets across all projects.
func (r *ProtocolLXD) GetStoragePoolBucketsAllProjects(poolName string) ([]api.StorageBucket, error) {
	err := r.CheckExtension("storage_buckets_all_projects")
	if err != nil {
		return nil, err
	}

	buckets := []api.StorageBucket{}

	u := api.NewURL().Path("storage-pools", poolName, "buckets").WithQuery("recursion", "1").WithQuery("all-projects", "true")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &buckets)
	if err != nil {
		return nil, err
	}

	return buckets, nil
}

// GetStoragePoolBucket returns a storage bucket entry for the provided pool and bucket name.
func (r *ProtocolLXD) GetStoragePoolBucket(poolName string, bucketName string) (*api.StorageBucket, string, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, "", err
	}

	bucket := api.StorageBucket{}

	// Fetch the raw value.
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &bucket)
	if err != nil {
		return nil, "", err
	}

	return &bucket, etag, nil
}

// CreateStoragePoolBucket defines a new storage bucket using the provided struct.
// If the server supports the storage_buckets_create_credentials API extension, the initial admin credentials
// are included in the returned operation's metadata under the "key" field.
func (r *ProtocolLXD) CreateStoragePoolBucket(poolName string, bucket api.StorageBucketsPost) (Operation, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("storage-pools", poolName, "buckets")

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err == nil {
		op, _, err = r.queryOperation(http.MethodPost, u.String(), bucket, "", true)
	} else {
		// Fallback to older behavior without operations.
		// When the server supports storage_buckets_create_credentials, decode the
		// admin credentials from the response body and attach them to the noop
		// operation's metadata so callers can retrieve them the same way.
		if r.CheckExtension("storage_buckets_create_credentials") == nil {
			var newKey api.StorageBucketKey
			_, err = r.queryStruct(http.MethodPost, u.String(), bucket, "", &newKey)
			if err != nil {
				return nil, err
			}

			op = noopOperation{metadata: map[string]any{"key": newKey}}
		} else {
			op = noopOperation{}
			_, _, err = r.query(http.MethodPost, u.String(), bucket, "")
		}
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateStoragePoolBucket updates the storage bucket to match the provided struct.
func (r *ProtocolLXD) UpdateStoragePoolBucket(poolName string, bucketName string, bucket api.StorageBucketPut, ETag string) (Operation, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPut, u.String(), bucket, ETag)
	} else {
		op, _, err = r.queryOperation(http.MethodPut, u.String(), bucket, ETag, true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolBucket deletes an existing storage bucket.
func (r *ProtocolLXD) DeleteStoragePoolBucket(poolName string, bucketName string) (Operation, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	} else {
		op, _, err = r.queryOperation(http.MethodDelete, u.String(), nil, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolBucketKeyNames returns a list of storage bucket key names.
func (r *ProtocolLXD) GetStoragePoolBucketKeyNames(poolName string, bucketName string) ([]string, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetStoragePoolBucketKeys returns a list of storage bucket keys for the provided pool and bucket.
func (r *ProtocolLXD) GetStoragePoolBucketKeys(poolName string, bucketName string) ([]api.StorageBucketKey, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	bucketKeys := []api.StorageBucketKey{}

	// Fetch the raw value.
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &bucketKeys)
	if err != nil {
		return nil, err
	}

	return bucketKeys, nil
}

// GetStoragePoolBucketKey returns a storage bucket key entry for the provided pool, bucket and key name.
func (r *ProtocolLXD) GetStoragePoolBucketKey(poolName string, bucketName string, keyName string) (*api.StorageBucketKey, string, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, "", err
	}

	bucketKey := api.StorageBucketKey{}

	// Fetch the raw value.
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys", keyName)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &bucketKey)
	if err != nil {
		return nil, "", err
	}

	return &bucketKey, etag, nil
}

// CreateStoragePoolBucketKey adds a key to a storage bucket.
// The generated key credentials are included in the returned operation's metadata under the "key" field.
func (r *ProtocolLXD) CreateStoragePoolBucketKey(poolName string, bucketName string, key api.StorageBucketKeysPost) (Operation, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys")

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err == nil {
		op, _, err = r.queryOperation(http.MethodPost, u.String(), key, "", true)
	} else {
		// Fallback to older behavior without operations.
		// Decode the key credentials from the response body and attach them to the
		// noop operation's metadata so callers can retrieve them consistently.
		var newKey api.StorageBucketKey
		_, err = r.queryStruct(http.MethodPost, u.String(), key, "", &newKey)
		if err != nil {
			return nil, err
		}

		op = noopOperation{metadata: map[string]any{"key": newKey}}
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateStoragePoolBucketKey updates an existing storage bucket key.
func (r *ProtocolLXD) UpdateStoragePoolBucketKey(poolName string, bucketName string, keyName string, key api.StorageBucketKeyPut, ETag string) (Operation, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys", keyName)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPut, u.String(), key, ETag)
	} else {
		op, _, err = r.queryOperation(http.MethodPut, u.String(), key, ETag, true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolBucketKey removes a key from a storage bucket.
func (r *ProtocolLXD) DeleteStoragePoolBucketKey(poolName string, bucketName string, keyName string) error {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys", keyName)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}
