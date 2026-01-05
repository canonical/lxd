package drivers

// powerStoreClient holds the PowerStore HTTP API client.
type powerStoreClient struct {
	driver *powerstore
}

// newPowerStoreClient creates a new instance of the PowerStore HTTP API client.
func newPowerStoreClient(driver *powerstore) *powerStoreClient {
	return &powerStoreClient{
		driver: driver,
	}
}
