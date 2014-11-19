package main

import (
	"fmt"
	"sync"
	"time"

	"code.google.com/p/go-uuid/uuid"
	"github.com/lxc/lxd"
)

var lock sync.Mutex
var operations map[string]lxd.Operation = make(map[string]lxd.Operation)

func CreateOperation(metadata lxd.Jmap, run func() error, cancel func() error) string {
	op := lxd.Operation{}
	op.CreatedAt = time.Now()
	op.UpdatedAt = op.CreatedAt
	op.SetStatus(lxd.Pending)
	op.StatusCode = lxd.StatusCodes[op.Status]
	op.ResourceUrl = fmt.Sprintf("/%s/operations/%s", lxd.ApiVersion, uuid.New())
	op.Metadata = metadata
	op.MayCancel = cancel != nil

	op.Run = run
	op.Cancel = cancel

	lock.Lock()
	operations[op.ResourceUrl] = op
	lock.Unlock()
	return op.ResourceUrl
}

func StartOperation(uri string) error {
	lock.Lock()
	op, ok := operations[uri]
	if !ok {
		lock.Unlock()
		return fmt.Errorf("operation %s doesn't exist", uri)
	}

	go func(op lxd.Operation) {
		err := op.Run()

		lock.Lock()
		op.SetResult(err)
		lock.Unlock()
	}(op)

	op.SetStatus(lxd.Running)
	lock.Unlock()

	return nil
}

func CancelOperation(uri string) error {
	lock.Lock()
	op, ok := operations[uri]
	if !ok {
		lock.Unlock()
		return fmt.Errorf("operation %s doesn't exist", uri)
	}

	if op.Cancel == nil {
		op.SetStatus(lxd.Done)
		lock.Unlock()
		return nil
	}

	cancel := op.Cancel
	op.SetStatus(lxd.Cancelling)
	lock.Unlock()

	err := cancel()

	lock.Lock()
	op.SetStatus(lxd.Cancelled)
	op.SetResult(err)
	lock.Unlock()

	return err
}
