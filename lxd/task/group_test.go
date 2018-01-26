package task_test

import (
	"testing"
	"time"

	"github.com/lxc/lxd/lxd/task"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
)

func TestGroup_Add(t *testing.T) {
	group := &task.Group{}
	ok := make(chan struct{})
	f := func(context.Context) { close(ok) }
	group.Add(f, task.Every(time.Second))
	group.Start()

	assertRecv(t, ok)

	assert.NoError(t, group.Stop(time.Second))
}

func TestGroup_StopUngracefully(t *testing.T) {
	group := &task.Group{}

	// Create a task function that hangs.
	ok := make(chan struct{})
	defer close(ok)
	f := func(context.Context) {
		ok <- struct{}{}
		<-ok
	}

	group.Add(f, task.Every(time.Second))
	group.Start()

	assertRecv(t, ok)

	assert.EqualError(t, group.Stop(time.Millisecond), "tasks 0 are still running")
}

// Assert that the given channel receives an object within a second.
func assertRecv(t *testing.T, ch chan struct{}) {
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no object received")
	}
}
