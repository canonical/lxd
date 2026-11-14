package lxd

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

// Test_ExecInstance_noWebsockets checks that a non-interactive exec whose operation exposes no
// websocket still signals completion through DataDone rather than waiting forever.
func Test_ExecInstance_noWebsockets(t *testing.T) {
	const operationResponse = `{
		"type": "async",
		"status": "Operation created",
		"status_code": 100,
		"operation": "/1.0/operations/8c5bb7ae-0bd8-4b1c-a3b5-8f5f0c8e5c0e",
		"metadata": {
			"id": "8c5bb7ae-0bd8-4b1c-a3b5-8f5f0c8e5c0e",
			"class": "task",
			"status": "Running",
			"status_code": 103,
			"metadata": {}
		}
	}`

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if req.Method == http.MethodPost && req.URL.Path == "/1.0/instances/c1/exec" {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(operationResponse))
			return
		}

		// Anything else (such as the events websocket) is not served.
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":"Not found","error_code":404}`))
	}))
	t.Cleanup(server.Close)

	client, err := ConnectLXDWithContext(t.Context(), server.URL, &ConnectionArgs{
		InsecureSkipVerify: true,
		SkipGetServer:      true,
	})
	require.NoError(t, err)
	t.Cleanup(client.Disconnect)

	dataDone := make(chan bool)

	op, err := client.ExecInstance("c1", api.InstanceExecPost{Command: []string{"true"}}, &InstanceExecArgs{DataDone: dataDone})
	require.NoError(t, err)
	require.NotNil(t, op)

	select {
	case <-dataDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ExecInstance() did not close DataDone when no websocket was connected")
	}
}
