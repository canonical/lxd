package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"code.google.com/p/go-uuid/uuid"
	"github.com/gorilla/mux"
	"github.com/lxc/lxd"
)

var lock sync.Mutex
var operations map[string]*lxd.Operation = make(map[string]*lxd.Operation)

func CreateOperation(metadata lxd.Jmap, run func() error, cancel func() error) string {
	id := uuid.New()
	op := lxd.Operation{}
	op.CreatedAt = time.Now()
	op.UpdatedAt = op.CreatedAt
	op.SetStatus(lxd.Pending)
	op.StatusCode = lxd.StatusCodes[op.Status]
	op.ResourceURL = lxd.OperationsURL(id)
	op.Metadata = metadata
	op.MayCancel = cancel != nil

	op.Run = run
	op.Cancel = cancel
	op.Chan = make(chan bool, 1)

	lock.Lock()
	operations[op.ResourceURL] = &op
	lock.Unlock()
	return op.ResourceURL
}

func StartOperation(id string) error {
	lock.Lock()
	op, ok := operations[id]
	if !ok {
		lock.Unlock()
		return fmt.Errorf("operation %s doesn't exist", id)
	}

	go func(op *lxd.Operation) {
		err := op.Run()

		lock.Lock()
		op.SetStatus(lxd.Done)
		op.SetResult(err)
		op.Chan <- true
		lock.Unlock()
	}(op)

	op.SetStatus(lxd.Running)
	lock.Unlock()

	return nil
}

func operationsGet(d *Daemon, r *http.Request) Response {
	ops := lxd.Jmap{"pending": make([]string, 0, 0), "running": make([]string, 0, 0)}

	lock.Lock()
	for k, v := range operations {
		switch v.Status {
		case lxd.Pending:
			ops["pending"] = append(ops["pending"].([]string), k)
		case lxd.Running:
			ops["running"] = append(ops["running"].([]string), k)
		}
	}
	lock.Unlock()

	return SyncResponse(true, ops)
}

var operationsCmd = Command{"operations", false, false, operationsGet, nil, nil, nil}

func operationGet(d *Daemon, r *http.Request) Response {
	id := lxd.OperationsURL(mux.Vars(r)["id"])

	lock.Lock()
	defer lock.Unlock()
	op, ok := operations[id]
	if !ok {
		return NotFound
	}

	return SyncResponse(true, op)
}

func operationDelete(d *Daemon, r *http.Request) Response {
	lock.Lock()
	id := lxd.OperationsURL(mux.Vars(r)["id"])
	op, ok := operations[id]
	if !ok {
		lock.Unlock()
		return NotFound
	}

	if op.Cancel == nil {
		return BadRequest(fmt.Errorf("Can't cancel %s!", id))
	}

	if op.Status == lxd.Done || op.Status == lxd.Cancelling || op.Status == lxd.Cancelled {
		/* the user has already requested a cancel */
		return EmptySyncResponse
	}

	cancel := op.Cancel
	op.SetStatus(lxd.Cancelling)
	lock.Unlock()

	err := cancel()

	lock.Lock()
	op.SetStatus(lxd.Cancelled)
	op.SetResult(err)
	lock.Unlock()

	if err != nil {
		return InternalError(err)
	} else {
		return EmptySyncResponse
	}
}

var operationCmd = Command{"operations/{id}", false, false, operationGet, nil, nil, operationDelete}

func operationWaitPost(d *Daemon, r *http.Request) Response {
	lock.Lock()
	id := lxd.OperationsURL(mux.Vars(r)["id"])
	op, ok := operations[id]
	if !ok {
		lock.Unlock()
		return NotFound
	}

	status := op.Status
	lock.Unlock()

	if status == lxd.Pending || status == lxd.Running {
		/* Wait until the routine is done */
		<-op.Chan
	}

	lock.Lock()
	defer lock.Unlock()
	return SyncResponse(true, op)
}

var operationWait = Command{"operations/{id}/wait", false, false, nil, nil, operationWaitPost, nil}
