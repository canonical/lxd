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
	operations[id] = &op
	lock.Unlock()
	return id
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

func operationsGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
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

	SyncResponse(true, ops, w)
}

var operationsCmd = Command{"operations", false, operationsGet, nil, nil, nil}

func operationGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	lock.Lock()
	op, ok := operations[id]
	if !ok {
		lock.Unlock()
		NotFound(w)
		return
	}

	SyncResponse(true, op, w)
	lock.Unlock()
}

func operationDelete(d *Daemon, w http.ResponseWriter, r *http.Request) {
	lock.Lock()
	id := mux.Vars(r)["id"]
	op, ok := operations[id]
	if !ok {
		lock.Unlock()
		NotFound(w)
		return
	}

	if op.Cancel == nil {
		op.SetStatus(lxd.Done)
		lock.Unlock()
		return
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
		InternalError(w, err)
	} else {
		EmptySyncResponse(w)
	}
}

var operationCmd = Command{"operations/{id}", false, operationGet, nil, nil, operationDelete}

func operationWaitPost(d *Daemon, w http.ResponseWriter, r *http.Request) {
	lock.Lock()
	id := mux.Vars(r)["id"]
	op, ok := operations[id]
	if !ok {
		lock.Unlock()
		NotFound(w)
		return
	}

	status := op.Status
	lock.Unlock()

	if status == lxd.Pending || status == lxd.Running {
		/* Wait until the routine is done */
		<-op.Chan
	}

	lock.Lock()
	SyncResponse(true, op, w)
	lock.Unlock()
}

var operationWait = Command{"operations/{id}/wait", false, nil, nil, operationWaitPost, nil}
