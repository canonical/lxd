package response

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

func TestSyncResponseCompressed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/1.0/test", nil)
	rec := httptest.NewRecorder()

	resp := SyncResponseCompressed(true, map[string]string{"key": "value"})
	require.NoError(t, resp.Render(rec, req))

	result := rec.Result()

	defer func() { _ = result.Body.Close() }()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	assert.Equal(t, "gzip", result.Header.Get("Content-Encoding"))
	assert.Contains(t, strings.ToLower(result.Header.Get("Vary")), "accept-encoding")
	assert.Contains(t, result.Header.Get("Content-Type"), "application/json")

	reader, err := gzip.NewReader(result.Body)
	require.NoError(t, err)

	body, err := io.ReadAll(reader)
	require.NoError(t, err)

	require.NoError(t, reader.Close())

	var parsed api.ResponseRaw
	require.NoError(t, json.Unmarshal(body, &parsed))

	assert.Equal(t, api.SyncResponse, parsed.Type)
	assert.Equal(t, api.Success.String(), parsed.Status)
	assert.Equal(t, int(api.Success), parsed.StatusCode)

	metadata, ok := parsed.Metadata.(map[string]any)
	require.Truef(t, ok, "Expected metadata as map, got %T", parsed.Metadata)

	value, ok := metadata["key"].(string)
	require.Truef(t, ok, "Expected metadata key %q to be a string, got %T", "key", metadata["key"])
	assert.Equal(t, "value", value)
}
