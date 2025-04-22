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
// If the server supports storage_buckets_create_credentials API extension, then this function will return the
// initial admin credentials. Otherwise it will be nil.
func (r *ProtocolLXD) CreateStoragePoolBucket(poolName string, bucket api.StorageBucketsPost) (*api.StorageBucketKey, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("storage-pools", poolName, "buckets")

	// Send the request and get the resulting key info (including generated keys).
	if r.CheckExtension("storage_buckets_create_credentials") == nil {
		var newKey api.StorageBucketKey
		_, err = r.queryStruct(http.MethodPost, u.String(), bucket, "", &newKey)
		if err != nil {
			return nil, err
		}

		return &newKey, nil
	}

	_, _, err = r.query(http.MethodPost, u.String(), bucket, "")
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// UpdateStoragePoolBucket updates the storage bucket to match the provided struct.
func (r *ProtocolLXD) UpdateStoragePoolBucket(poolName string, bucketName string, bucket api.StorageBucketPut, ETag string) error {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName)
	_, _, err = r.query(http.MethodPut, u.String(), bucket, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteStoragePoolBucket deletes an existing storage bucket.
func (r *ProtocolLXD) DeleteStoragePoolBucket(poolName string, bucketName string) error {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
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
func (r *ProtocolLXD) CreateStoragePoolBucketKey(poolName string, bucketName string, key api.StorageBucketKeysPost) (*api.StorageBucketKey, error) {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return nil, err
	}

	// Send the request and get the resulting key info (including generated keys).
	var newKey api.StorageBucketKey
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys")
	_, err = r.queryStruct(http.MethodPost, u.String(), key, "", &newKey)
	if err != nil {
		return nil, err
	}

	return &newKey, err
}

// UpdateStoragePoolBucketKey updates an existing storage bucket key.
func (r *ProtocolLXD) UpdateStoragePoolBucketKey(poolName string, bucketName string, keyName string, key api.StorageBucketKeyPut, ETag string) error {
	err := r.CheckExtension("storage_buckets")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("storage-pools", poolName, "buckets", bucketName, "keys", keyName)
	_, _, err = r.query(http.MethodPut, u.String(), key, ETag)
	if err != nil {
		return err
	}

	return nil
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
