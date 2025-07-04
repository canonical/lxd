package drivers

// pureClient holds the Pure Storage HTTP client and an access token.
type hpeAlletraClient struct {
	driver     *hpeAlletra
	sessionKey string
}

// newPureClient creates a new instance of the HTTP Pure Storage client.
func newHPEAlletraClient(driver *hpeAlletra) *hpeAlletraClient {
	return &hpeAlletraClient{
		driver: driver,
	}
}
