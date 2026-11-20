package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentAPI "github.com/canonical/lxd/lxd-agent/api"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/shared/api"
)

func eventsPostTest(t *testing.T, ctx context.Context, eventType string, metadata any) (*httptest.ResponseRecorder, api.Response) {
	t.Helper()

	eventServer, err := events.NewServer(false, false, nil)
	require.NoError(t, err)

	encodedMetadata, err := json.Marshal(metadata)
	require.NoError(t, err)

	body, err := json.Marshal(api.Event{Type: eventType, Metadata: encodedMetadata})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/1.0/events", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	resp := eventsPost(&Daemon{events: eventServer}, req)
	require.NoError(t, resp.Render(rec, req))

	var apiResp api.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &apiResp))

	return rec, apiResp
}

func invalidDiskEvent(action agentAPI.DeviceEventAction) map[string]any {
	return map[string]any{
		"action": action,
		"name":   "d1",
		"config": map[string]string{"type": "disk", "source": "/tmp", "path": "/mnt/../d1"},
	}
}

func TestEventsPostDeviceAddedFailure(t *testing.T) {
	rec, apiResp := eventsPostTest(t, context.Background(), "device", invalidDiskEvent(agentAPI.DeviceAdded))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, api.ErrorResponse, apiResp.Type)
	assert.Equal(t, `Invalid path "/mnt/../d1": Path must not contain '..'`, apiResp.Error)
}

func TestEventsPostDeviceRemovedFailure(t *testing.T) {
	rec, apiResp := eventsPostTest(t, context.Background(), "device", invalidDiskEvent(agentAPI.DeviceRemoved))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, api.SyncResponse, apiResp.Type)
}

func TestEventsPostNonDeviceEvent(t *testing.T) {
	rec, apiResp := eventsPostTest(t, context.Background(), "config", map[string]string{"key": "user.foo", "value": "bar"})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, api.SyncResponse, apiResp.Type)
}

func TestEventsPostDeviceAddedCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	event := map[string]any{
		"action": agentAPI.DeviceAdded,
		"name":   "d1",
		"config": map[string]string{"type": "disk", "source": "/tmp", "path": filepath.Join(t.TempDir(), "d1")},
	}

	rec, apiResp := eventsPostTest(t, ctx, "device", event)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, api.ErrorResponse, apiResp.Type)
	assert.Equal(t, `Failed hotpluging device "lxd_d1": context canceled`, apiResp.Error)
}
