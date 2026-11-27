package operationlock

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopRequestedOnInheritedRestart(t *testing.T) {
	restartOp, err := Create("default", "c1", ActionRestart, true, true)
	require.NoError(t, err)
	defer restartOp.Done(nil)

	assert.False(t, restartOp.GetStopRequested())

	// A stop inherits the running restart operation, the same way Stop() does in the drivers.
	stopOp, err := CreateWaitGet("default", "c1", ActionStop, []Action{ActionRestart}, false, true)
	require.NoError(t, err)
	assert.Same(t, restartOp, stopOp)
	assert.Equal(t, ActionRestart, stopOp.Action())

	stopOp.SetStopRequested()
	assert.True(t, restartOp.GetStopRequested())
}

func TestStopRequestedNilOperation(t *testing.T) {
	var op *InstanceOperation

	op.SetStopRequested()
	assert.False(t, op.GetStopRequested())
}
