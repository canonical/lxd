package api

// StorageBucketsPost represents the fields of a new LXD storage pool bucket
//
// swagger:model
//
// API extension: storage_buckets.
type StorageBucketsPost struct {
	StorageBucketPut `yaml:",inline"`

	// Bucket name
	// Example: foo
	//
	// API extension: storage_buckets
	Name string `json:"name" yaml:"name"`
}

// StorageBucketPut represents the modifiable fields of a LXD storage pool bucket
//
// swagger:model
//
// API extension: storage_buckets.
type StorageBucketPut struct {
	// Storage bucket configuration map
	// Example: {"size": "50GiB"}
	//
	// API extension: storage_buckets
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the storage bucket
	// Example: My custom bucket
	//
	// API extension: storage_buckets
	Description string `json:"description" yaml:"description"`
}

// StorageBucket represents the fields of a LXD storage pool bucket
//
// swagger:model
//
// API extension: storage_buckets.
type StorageBucket struct {
	WithEntitlements `yaml:",inline"` //nolint:musttag

	// Bucket name
	// Example: foo
	//
	// API extension: storage_buckets
	Name string `json:"name" yaml:"name"`

	// Description of the storage bucket
	// Example: My custom bucket
	//
	// API extension: storage_buckets
	Description string `json:"description" yaml:"description"`

	// Bucket S3 URL
	// Example: https://127.0.0.1:8080/foo
	//
	// API extension: storage_buckets
	S3URL string `json:"s3_url" yaml:"s3_url"`

	// What cluster member this record was found on
	// Example: lxd01
	//
	// API extension: storage_buckets
	Location string `json:"location" yaml:"location"`

	// Storage bucket configuration map
	// Example: {"size": "50GiB"}
	//
	// API extension: storage_buckets
	Config map[string]string `json:"config" yaml:"config"`

	// Project name
	// Example: project1
	//
	// API extension: storage_buckets_all_projects
	Project string `json:"project" yaml:"project"`
}

// Etag returns the values used for etag generation.
func (b *StorageBucket) Etag() []any {
	return []any{b.Name, b.Description, b.Config}
}

// Writable converts a full StorageBucket struct into a StorageBucketPut struct (filters read-only fields).
func (b *StorageBucket) Writable() StorageBucketPut {
	return StorageBucketPut{
		Description: b.Description,
		Config:      b.Config,
	}
}

// SetWritable sets applicable values from StorageBucketPut struct to StorageBucket struct.
func (b *StorageBucket) SetWritable(put StorageBucketPut) {
	b.Description = put.Description
	b.Config = put.Config
}

// URL returns the URL for the bucket.
func (b *StorageBucket) URL(apiVersion string, poolName string, projectName string) *URL {
	return NewURL().Path(apiVersion, "storage-pools", poolName, "buckets", b.Name).Project(projectName).Target(b.Location)
}

// StorageBucketKeysPost represents the fields of a new LXD storage pool bucket key
//
// swagger:model
//
// API extension: storage_buckets.
type StorageBucketKeysPost struct {
	StorageBucketKeyPut `yaml:",inline"`

	// Key name
	// Example: my-read-only-key
	//
	// API extension: storage_buckets
	Name string `json:"name" yaml:"name"`
}

// StorageBucketKeyPut represents the modifiable fields of a LXD storage pool bucket key
//
// swagger:model
//
// API extension: storage_buckets.
type StorageBucketKeyPut struct {
	// Description of the storage bucket key
	// Example: My read-only bucket key
	//
	// API extension: storage_buckets
	Description string `json:"description" yaml:"description"`

	// Whether the key can perform write actions or not.
	// Example: read-only
	//
	// API extension: storage_buckets
	Role string `json:"role" yaml:"role"`

	// Access key
	// Example: 33UgkaIBLBIxb7O1
	//
	// API extension: storage_buckets
	AccessKey string `json:"access-key" yaml:"access-key"`

	// Secret key
	// Example: kDQD6AOgwHgaQI1UIJBJpPaiLgZuJbq0
	//
	// API extension: storage_buckets
	SecretKey string `json:"secret-key" yaml:"secret-key"`
}

// StorageBucketKey represents the fields of a LXD storage pool bucket key
//
// swagger:model
//
// API extension: storage_buckets.
type StorageBucketKey struct {
	// Key name
	// Example: my-read-only-key
	//
	// API extension: storage_buckets
	Name string `json:"name" yaml:"name"`

	// Description of the storage bucket key
	// Example: My read-only bucket key
	//
	// API extension: storage_buckets
	Description string `json:"description" yaml:"description"`

	// Whether the key can perform write actions or not.
	// Example: read-only
	//
	// API extension: storage_buckets
	Role string `json:"role" yaml:"role"`

	// Access key
	// Example: 33UgkaIBLBIxb7O1
	//
	// API extension: storage_buckets
	AccessKey string `json:"access-key" yaml:"access-key"`

	// Secret key
	// Example: kDQD6AOgwHgaQI1UIJBJpPaiLgZuJbq0
	//
	// API extension: storage_buckets
	SecretKey string `json:"secret-key" yaml:"secret-key"`
}

// URL for the deployment instance set.
func (b *StorageBucketKey) URL(apiVersion string, poolName string, projectName string, bucketName string) *URL {
	return NewURL().Path(apiVersion, "storage-pools", poolName, "buckets", bucketName, "keys", b.Name).Project(projectName)
}

// Etag returns the values used for etag generation.
func (b *StorageBucketKey) Etag() []any {
	return []any{b.Name, b.Description, b.Role, b.AccessKey, b.SecretKey}
}

// Writable converts a full StorageBucketKey struct into a StorageBucketKeyPut struct (filters read-only fields).
func (b *StorageBucketKey) Writable() StorageBucketKeyPut {
	return StorageBucketKeyPut{
		Description: b.Description,
		Role:        b.Role,
		AccessKey:   b.AccessKey,
		SecretKey:   b.SecretKey,
	}
}

// SetWritable sets applicable values from StorageBucketKeyPut struct to StorageBucketKey struct.
func (b *StorageBucketKey) SetWritable(put StorageBucketKeyPut) {
	b.Description = put.Description
	b.Role = put.Role
	b.AccessKey = put.AccessKey
	b.SecretKey = put.SecretKey
}
