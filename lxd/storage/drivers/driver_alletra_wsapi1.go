package drivers

// alletraClient holds the HPE Alletra Storage HTTP client and an access token.
type alletraClient struct {
	driver *alletra
}

// newAlletraClient creates a new instance of the HPE Alletra Storage HTTP client.
func newAlletraClient(driver *alletra) *alletraClient {
	return &alletraClient{
		driver: driver,
	}
}
