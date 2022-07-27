package drivers

// Bucket represents a storage bucket.
type Bucket struct {
	name       string
	pool       string
	poolConfig map[string]string
	config     map[string]string
	driver     Driver
}

// NewBucket instantiates a new Bucket struct.
func NewBucket(driver Driver, poolName string, bucketName string, bucketConfig map[string]string, poolConfig map[string]string) Bucket {
	return Bucket{
		name:       bucketName,
		pool:       poolName,
		poolConfig: poolConfig,
		config:     bucketConfig,
		driver:     driver,
	}
}

// Name returns volume's name.
func (b Bucket) Name() string {
	return b.name
}

// Pool returns the volume's pool name.
func (b Bucket) Pool() string {
	return b.pool
}

// Config returns the volume's (unexpanded) config.
func (b Bucket) Config() map[string]string {
	return b.config
}
