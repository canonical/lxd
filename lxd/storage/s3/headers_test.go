package s3

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAuthorizationHeaderAccessKey tests the AuthorizationHeaderAccessKey function.
func TestAuthorizationHeaderAccessKey(t *testing.T) {
	// Test with a valid AWS4-HMAC-SHA256 Authorization header.
	headers := "AWS4-HMAC-SHA256 Credential=PRL470D7Q93X1ZA1L82X/20220825/US/s3/aws4_request,SignedHeaders=host;x-amz-content-sha256;x-amz-date,Signature=d8fdaf67c5072d4ff7ac56e4529e66fb08255aaa79193b212cba4670d058fade"
	expected := "PRL470D7Q93X1ZA1L82X"
	accessKey := AuthorizationHeaderAccessKey(headers)
	assert.Equal(t, expected, accessKey, "Expected access key to match for AWS4-HMAC-SHA256 header")

	// Test with slightly broken AWS4-HMAC-SHA256 Authorization header.
	headers = "AWS4-HMAC-SHA256 Credential=PRL470D7Q93X1ZA1L82X_20220825/US/s3/aws4_request,SignedHeaders=host;x-amz-content-sha256;x-amz-date,Signature=d8fdaf67c5072d4ff7ac56e4529e66fb08255aaa79193b212cba4670d058fade"
	accessKey = AuthorizationHeaderAccessKey(headers)
	assert.Empty(t, accessKey, "Expected access key to be empty for broken AWS4-HMAC-SHA256 header")

	// Test with an older AWS Authorization header.
	headers = "AWS PRL470D7Q93X1ZA1L82X:dC5GcyRFCyQIr+y9BdpAwBjkOK0="
	expected = "PRL470D7Q93X1ZA1L82X"
	accessKey = AuthorizationHeaderAccessKey(headers)
	assert.Equal(t, expected, accessKey, "Expected access key to match for older AWS header")

	// Test with an invalid Authorization header.
	headers = "InvalidHeaderFormat"
	accessKey = AuthorizationHeaderAccessKey(headers)
	assert.Empty(t, accessKey, "Expected access key to be empty for invalid header")
}
