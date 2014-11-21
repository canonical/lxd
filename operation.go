package lxd

import (
	"fmt"
	"time"
)

type OperationStatus string

const (
	Pending    OperationStatus = "pending"
	Running                    = "running"
	Done                       = "done"
	Cancelling                 = "cancelling"
	Cancelled                  = "cancelled"
)

var StatusCodes = map[OperationStatus]int{
	Pending:    0,
	Running:    1,
	Done:       2,
	Cancelling: 3,
	Cancelled:  4,
}

type Result string

const (
	Success Result = "success"
	Failure        = "failure"
)

var ResultCodes = map[Result]int{
	Failure: 0,
	Success: 1,
}

type Operation struct {
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Status      OperationStatus `json:"status"`
	StatusCode  int             `json:"status_code"`
	Result      Result          `json:"result"`
	ResultCode  int             `json:"result_code"`
	ResourceURL string          `json:"resource_url"`
	Metadata    Jmap            `json:"metadata"`
	MayCancel   bool            `json:"may_cancel"`

	Run    func() error `json:"-"`
	Cancel func() error `json:"-"`

	/* This channel receives exactly one value, when the event is done and
	 * the status is updated */
	Chan chan bool `json:"-"`
}

func (o *Operation) SetStatus(status OperationStatus) {
	o.Status = status
	o.StatusCode = StatusCodes[status]
	o.UpdatedAt = time.Now()
}

func (o *Operation) SetResult(err error) {
	if err == nil {
		o.Result = Success
		o.ResultCode = ResultCodes[Success]
	} else {
		o.Result = Failure
		o.ResultCode = ResultCodes[Failure]
	}
	o.UpdatedAt = time.Now()
}

func OperationsURL(id string) string {
	return fmt.Sprintf("/%s/operations/%s", ApiVersion, id)
}
