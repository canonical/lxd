package shared

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var WebsocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// OperationWebsocket represents the /websocket endpoint for operations. Users
// can connect by specifying a secret (given to them at operation creation
// time). As soon as the operation is created, the websocket's Do() function is
// called. It is up to the Do() function to block and wait for any connections
// it expects before proceeding.
type OperationWebsocket interface {

	// Metadata() specifies the metadata for the initial response this
	// OperationWebsocket renders.
	Metadata() interface{}

	// Connect should return the error if the connection failed,
	// or nil if the connection was successful.
	Connect(secret string, r *http.Request, w http.ResponseWriter) error

	// Run the actual operation and return its result.
	Do() OperationResult
}

type OperationResult struct {
	Metadata json.RawMessage
	Error    error
}

var OperationSuccess OperationResult = OperationResult{}

func OperationWrap(f func() error) func() OperationResult {
	return func() OperationResult { return OperationError(f()) }
}

func OperationError(err error) OperationResult {
	return OperationResult{nil, err}
}

type Operation struct {
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Status     string              `json:"status"`
	StatusCode StatusCode          `json:"status_code"`
	Resources  map[string][]string `json:"resources"`
	Metadata   json.RawMessage     `json:"metadata"`
	MayCancel  bool                `json:"may_cancel"`

	/* The fields below are for use on the server side. */
	Run func() OperationResult `json:"-"`

	/* If this is not nil, the operation can be cancelled by calling this
	 * function */
	Cancel func() error `json:"-"`

	/* This channel receives exactly one value, when the event is done and
	 * the status is updated */
	Chan chan bool `json:"-"`

	/* If this is not nil, users can connect to a websocket for this
	 * operation. The flag indicates whether or not this socket has already
	 * been used: websockets can be connected to exactly once. */
	Websocket OperationWebsocket `json:"-"`
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

func (o *Operation) MetadataAsMap() (*Jmap, error) {
	ret := Jmap{}
	if err := json.Unmarshal(o.Metadata, &ret); err != nil {
		return nil, err
	}
	return &ret, nil
}

func (o *Operation) SetStatus(status StatusCode) {
	o.Status = status.String()
	o.StatusCode = status
	o.UpdatedAt = time.Now()
	if status.IsFinal() {
		o.MayCancel = false
		/*
		 * These cannot be reused once a status is final. Further, they
		 * are often pointers to functions that were/are members of
		 * some struct that is holding on to an lxdContainer struct,
		 * which keeps the log fds open as long as it is around. Let's
		 * make sure we don't "leak" these.
		 */
		o.Cancel = nil
		o.Run = nil
		o.Websocket = nil
	}
}

func (o *Operation) SetResult(result OperationResult) {
	o.SetStatusByErr(result.Error)
	if result.Metadata != nil {
		o.Metadata = result.Metadata
	}
	o.Chan <- true
}

func (o *Operation) SetStatusByErr(err error) {
	if err == nil {
		o.SetStatus(Success)
	} else {
		o.SetStatus(Failure)
		md, err := json.Marshal(err.Error())

		/* This isn't really fatal, it'll just be annoying for users */
		if err != nil {
			Debugf("Error converting %s to json", err)
		}
		o.Metadata = md
	}
}

func OperationsURL(id string) string {
	return fmt.Sprintf("/%s/operations/%s", APIVersion, id)
}
