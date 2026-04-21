package clients

// PowerStoreClient holds the PowerStore HTTP API client.
type PowerStoreClient struct {
	url                string
	skipTLSVerify      bool
	username           string
	password           string
	resourceNamePrefix string
}

// NewPowerStoreClient creates a new instance of the PowerStore HTTP API client.
func NewPowerStoreClient(url string, username string, password string, skipTLSVerify bool, resourceNamePrefix string) *PowerStoreClient {
	return &PowerStoreClient{
		url:                url,
		skipTLSVerify:      skipTLSVerify,
		username:           username,
		password:           password,
		resourceNamePrefix: resourceNamePrefix,
	}
}
