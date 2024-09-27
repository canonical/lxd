package drivers

// pureClient holds the PureStorage HTTP client and an access token.
type pureClient struct {
	driver      *pure
	accessToken string
}

// newPureClient creates a new instance of the HTTP PureStorage client.
func newPureClient(driver *pure) *pureClient {
	return &pureClient{
		driver: driver,
	}
}
