package lxd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/units"
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
	_, err = r.queryStruct("GET", u.String(), nil, "", &urls)
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
	_, err = r.queryStruct("GET", u.String(), nil, "", &buckets)
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
	etag, err := r.queryStruct("GET", u.String(), nil, "", &bucket)
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
		_, err = r.queryStruct("POST", u.String(), bucket, "", &newKey)
		if err != nil {
			return nil, err
		}

		return &newKey, nil
	}

	_, _, err = r.query("POST", u.String(), bucket, "")
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
	_, _, err = r.query("PUT", u.String(), bucket, ETag)
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
	_, _, err = r.query("DELETE", u.String(), nil, "")
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
	_, err = r.queryStruct("GET", u.String(), nil, "", &urls)
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
	_, err = r.queryStruct("GET", u.String(), nil, "", &bucketKeys)
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
	etag, err := r.queryStruct("GET", u.String(), nil, "", &bucketKey)
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
	_, err = r.queryStruct("POST", u.String(), key, "", &newKey)
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
	_, _, err = r.query("PUT", u.String(), key, ETag)
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
	_, _, err = r.query("DELETE", u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateStoragePoolBucketBackup creates a new storage bucket backup.
func (r *ProtocolLXD) CreateStoragePoolBucketBackup(poolName string, bucketName string, backup api.StorageBucketBackupsPost) (Operation, error) {
	err := r.CheckExtension("storage_bucket_backup")
	if err != nil {
		return nil, err
	}

	op, _, err := r.queryOperation("POST", "/storage-pools/"+url.PathEscape(poolName)+"/buckets/"+url.PathEscape(bucketName)+"/backups", backup, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteStoragePoolBucketBackup deletes an existing storage bucket backup.
func (r *ProtocolLXD) DeleteStoragePoolBucketBackup(pool string, bucketName string, name string) (Operation, error) {
	err := r.CheckExtension("storage_bucket_backup")
	if err != nil {
		return nil, err
	}

	op, _, err := r.queryOperation("DELETE", "/storage-pools/"+url.PathEscape(pool)+"/buckets/"+url.PathEscape(bucketName)+"/backups/"+url.PathEscape(name), nil, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetStoragePoolBucketBackupFile returns the storage bucket file.
func (r *ProtocolLXD) GetStoragePoolBucketBackupFile(pool string, bucketName string, name string, req *BackupFileRequest) (*BackupFileResponse, error) {
	err := r.CheckExtension("storage_bucket_backup")
	if err != nil {
		return nil, err
	}

	// Build the URL
	uri := r.httpBaseURL.String() + "/1.0/storage-pools/" + url.PathEscape(pool) + "/buckets/" + url.PathEscape(bucketName) + "/backups/" + url.PathEscape(name) + "/export"

	if r.project != "" {
		uri += "?project=" + url.QueryEscape(r.project)
	}

	// Prepare the download request
	request, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	if r.httpUserAgent != "" {
		request.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Start the request
	response, doneCh, err := cancel.CancelableDownload(req.Canceler, r.DoHTTP, request)
	if err != nil {
		return nil, err
	}

	defer func() { _ = response.Body.Close() }()
	defer close(doneCh)

	if response.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(response)
		if err != nil {
			return nil, err
		}
	}

	// Handle the data
	body := response.Body
	if req.ProgressHandler != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: response.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: response.ContentLength,
				Handler: func(percent int64, speed int64) {
					req.ProgressHandler(ioprogress.ProgressData{Text: strconv.FormatInt(percent, 10) + "% (" + units.GetByteSizeString(speed, 2) + "/s)"})
				},
			},
		}
	}

	size, err := io.Copy(req.BackupFile, body)
	if err != nil {
		return nil, err
	}

	resp := BackupFileResponse{}
	resp.Size = size

	return &resp, nil
}

// CreateStoragePoolBucketFromBackup creates a storage pool bucket using a backup.
func (r *ProtocolLXD) CreateStoragePoolBucketFromBackup(pool string, args StoragePoolBucketBackupArgs) (Operation, error) {
	if !r.HasExtension("storage_bucket_backup") {
		return nil, fmt.Errorf(`The server is missing the required "custom_volume_backup" API extension`)
	}

	path := "/storage-pools/" + url.PathEscape(pool) + "/buckets"

	// Prepare the HTTP request.
	reqURL, err := r.setQueryAttributes(r.httpBaseURL.String() + "/1.0" + path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	if args.Name != "" {
		req.Header.Set("X-LXD-name", args.Name)
	}

	// Send the request.
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	// Handle errors.
	response, _, err := lxdParseResponse(resp)
	if err != nil {
		return nil, err
	}

	respOperation, err := response.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	op := operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	return &op, nil
}
