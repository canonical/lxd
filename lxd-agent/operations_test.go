package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentOperations "github.com/canonical/lxd/lxd-agent/operations"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/shared/api"
)

func TestOperationWaitGetFailure(t *testing.T) {
	op, err := agentOperations.ScheduleOperation(nil, agentOperations.OperationArgs{
		Type:  operationtype.CommandExec,
		Class: operationtype.OperationClassWebsocket,
		RunHook: func(context.Context, *agentOperations.Operation) error {
			return api.NewStatusError(http.StatusConflict, "test operation failure")
		},
		ConnectHook: func(*agentOperations.Operation, *http.Request, http.ResponseWriter) error {
			return nil
		},
	})
	require.NoError(t, err)

	_, opAPI := op.Render()
	req := httptest.NewRequest(http.MethodGet, "/1.0/operations/"+opAPI.ID+"/wait?timeout=5", nil)
	req.SetPathValue("id", opAPI.ID)
	rec := httptest.NewRecorder()

	resp := operationWaitGet(nil, req)
	require.NoError(t, resp.Render(rec, req))
	assert.Equal(t, http.StatusOK, rec.Code)

	var apiResp api.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &apiResp))
	assert.Equal(t, api.SyncResponse, apiResp.Type)

	waitedOp, err := apiResp.MetadataAsOperation()
	require.NoError(t, err)
	assert.Equal(t, api.Failure.String(), waitedOp.Status)
	assert.Equal(t, api.Failure, waitedOp.StatusCode)
	assert.Equal(t, "test operation failure", waitedOp.Err)
	assert.Equal(t, int64(http.StatusConflict), waitedOp.ErrCode)
}

func TestOperationWaitGetTimeout(t *testing.T) {
	op, err := agentOperations.ScheduleOperation(nil, agentOperations.OperationArgs{
		Type:  operationtype.CommandExec,
		Class: operationtype.OperationClassWebsocket,
		RunHook: func(ctx context.Context, _ *agentOperations.Operation) error {
			<-ctx.Done()
			return ctx.Err()
		},
		ConnectHook: func(*agentOperations.Operation, *http.Request, http.ResponseWriter) error {
			return nil
		},
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		op.Cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.ErrorIs(t, op.Wait(ctx), context.Canceled)
	})

	_, opAPI := op.Render()
	req := httptest.NewRequest(http.MethodGet, "/1.0/operations/"+opAPI.ID+"/wait?timeout=0", nil)
	req.SetPathValue("id", opAPI.ID)
	rec := httptest.NewRecorder()

	resp := operationWaitGet(nil, req)
	require.NoError(t, resp.Render(rec, req))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var apiResp api.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &apiResp))
	assert.Equal(t, api.ErrorResponse, apiResp.Type)
	assert.Equal(t, "context deadline exceeded", apiResp.Error)
}
