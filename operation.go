package lxd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type OperationStatus int

const (
	OK         OperationStatus = 100
	Started    OperationStatus = 101
	Stopped    OperationStatus = 102
	Running    OperationStatus = 103
	Cancelling OperationStatus = 104
	Pending    OperationStatus = 105

	Success OperationStatus = 200

	Failure   OperationStatus = 400
	Cancelled OperationStatus = 401
)

func (o OperationStatus) String() string {
	return map[OperationStatus]string{
		OK:         "OK",
		Started:    "Started",
		Stopped:    "Stopped",
		Running:    "Running",
		Cancelling: "Cancelling",
		Pending:    "Pending",
		Success:    "Success",
		Failure:    "Failure",
		Cancelled:  "Cancelled",
	}[o]
}

func (o OperationStatus) IsFinal() bool {
	return int(o) >= 200
}

var WebsocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type OperationSocket interface {
	Secret() string
	Do(conn *websocket.Conn)
}

type Operation struct {
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Status      string          `json:"status"`
	StatusCode  OperationStatus `json:"status_code"`
	ResourceURL string          `json:"resource_url"`
	Metadata    json.RawMessage `json:"metadata"`
	MayCancel   bool            `json:"may_cancel"`

	/* The fields below are for use on the server side. */
	Run func() error `json:"-"`

	/* If this is not nil, the operation can be cancelled by calling this
	 * function */
	Cancel func() error `json:"-"`

	/* This channel receives exactly one value, when the event is done and
	 * the status is updated */
	Chan chan bool `json:"-"`

	/* If this is not nil, users can connect to a websocket for this
	 * operation. The flag indicates whether or not this socket has already
	 * been used: websockets can be connected to exactly once. */
	WebsocketConnected bool            `json:"-"`
	Websocket          OperationSocket `json:"-"`
}

func (o *Operation) GetError() error {
	if o.StatusCode == Failure {
		var s string
		if err := json.Unmarshal(o.Metadata, &s); err != nil {
			return err
		}

		return fmt.Errorf(s)
	}
	return nil
}

func (o *Operation) SetStatus(status OperationStatus) {
	o.Status = status.String()
	o.StatusCode = status
	o.UpdatedAt = time.Now()
	if status.IsFinal() {
		o.MayCancel = false
	}
}

func (o *Operation) SetStatusByErr(err error) {
	if err == nil {
		o.SetStatus(Success)
	} else {
		o.SetStatus(Failure)
		md, err := json.Marshal(err.Error())

		/* This isn't really fatal, it'll just be annoying for users */
		if err != nil {
			Debugf("error converting %s to json", err)
		}
		o.Metadata = md
	}
}

func OperationsURL(id string) string {
	return fmt.Sprintf("/%s/operations/%s", APIVersion, id)
}
