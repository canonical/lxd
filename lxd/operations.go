package main

import (
	"encoding/json"
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

func CreateOperation(metadata lxd.Jmap, run func() error, cancel func() error, ws lxd.OperationSocket) (string, error) {
	id := uuid.New()
	op := lxd.Operation{}
	op.CreatedAt = time.Now()
	op.UpdatedAt = op.CreatedAt
	op.SetStatus(lxd.Pending)
	op.ResourceURL = lxd.OperationsURL(id)

	md, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	op.Metadata = md

	op.MayCancel = cancel != nil

	op.Run = run
	op.Cancel = cancel
	op.Chan = make(chan bool, 1)
	op.WebsocketConnected = false
	op.Websocket = ws

	lock.Lock()
	operations[op.ResourceURL] = &op
	lock.Unlock()
	return op.ResourceURL, nil
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
		op.SetStatusByErr(err)
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
		switch v.StatusCode {
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

	if op.StatusCode == lxd.Cancelling || op.StatusCode.IsFinal() {
		/* the user has already requested a cancel, or the status is
		 * in a final state. */
		return EmptySyncResponse
	}

	cancel := op.Cancel
	op.SetStatus(lxd.Cancelling)
	lock.Unlock()

	err := cancel()

	lock.Lock()
	op.SetStatusByErr(err)
	lock.Unlock()

	if err != nil {
		return InternalError(err)
	} else {
		return EmptySyncResponse
	}
}

var operationCmd = Command{"operations/{id}", false, false, operationGet, nil, nil, operationDelete}

func operationWaitGet(d *Daemon, r *http.Request) Response {
	lock.Lock()
	id := lxd.OperationsURL(mux.Vars(r)["id"])
	op, ok := operations[id]
	if !ok {
		lock.Unlock()
		return NotFound
	}

	status := op.StatusCode
	lock.Unlock()

	targetStatus, err := lxd.AtoiEmptyDefault(r.FormValue("status_code"), int(lxd.Success))
	if err != nil {
		return InternalError(err)
	}

	timeout, err := lxd.AtoiEmptyDefault(r.FormValue("timeout"), -1)
	if err != nil {
		return InternalError(err)
	}

	if int(status) != targetStatus && status == lxd.Pending || status == lxd.Running {
		if timeout >= 0 {
			select {
			case <-op.Chan:
				break
			case <-time.After(time.Duration(timeout) * time.Second):
				break
			}
		} else {
			<-op.Chan
		}
	}

	lock.Lock()
	defer lock.Unlock()
	return SyncResponse(true, op)
}

var operationWait = Command{"operations/{id}/wait", false, false, operationWaitGet, nil, nil, nil}

type websocketServe struct {
	req *http.Request
	ws  lxd.OperationSocket
}

func (r *websocketServe) Render(w http.ResponseWriter) error {
	conn, err := lxd.WebsocketUpgrader.Upgrade(w, r.req, nil)
	if err != nil {
		return err
	}

	r.ws.Do(conn)
	conn.Close()

	return nil
}

func operationWebsocketGet(d *Daemon, r *http.Request) Response {
	lock.Lock()
	defer lock.Unlock()
	id := lxd.OperationsURL(mux.Vars(r)["id"])
	op, ok := operations[id]
	if !ok {
		return NotFound
	}

	ws := op.Websocket
	if ws == nil {
		return BadRequest(fmt.Errorf("operation has no websocket protocol"))
	}

	secret := r.FormValue("secret")
	if secret == "" {
		return BadRequest(fmt.Errorf("missing secret"))
	}

	if secret != ws.Secret() {
		return Forbidden
	}

	if op.StatusCode.IsFinal() {
		return BadRequest(fmt.Errorf("status is %s, can't connect", op.Status))
	}

	if op.WebsocketConnected {
		return BadRequest(fmt.Errorf("websocket already connected"))
	}

	op.WebsocketConnected = true

	return &websocketServe{r, ws}
}

var operationWebsocket = Command{"operations/{id}/websocket", false, false, operationWebsocketGet, nil, nil, nil}
