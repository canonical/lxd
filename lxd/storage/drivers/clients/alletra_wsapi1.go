package clients

// AlletraClient holds the HPE Alletra Storage HTTP client and an access token.
type AlletraClient struct {
}

// NewAlletraClient creates a new instance of the HPE Alletra Storage HTTP client.
func NewAlletraClient() *AlletraClient {
	return &AlletraClient{}
}
