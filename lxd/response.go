package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"

	"github.com/lxc/lxd"
)

type resp struct {
	Type     string      `json:"type"`
	Result   string      `json:"result"`
	Metadata interface{} `json:"metadata"`
}

type Response interface {
	Render(w http.ResponseWriter) error
}

type syncResponse struct {
	success  bool
	metadata interface{}
}

func (r *syncResponse) Render(w http.ResponseWriter) error {
	result := "success"
	if !r.success {
		result = "failure"
	}

	resp := resp{Type: string(lxd.Sync), Result: result, Metadata: r.metadata}
	enc, err := json.Marshal(&resp)
	if err != nil {
		return err
	}
	lxd.Debugf(string(enc))

	_, err = w.Write(enc)
	return err
}

/*
 * This function and AsyncResponse are simply wrappers for the response so
 * users don't have to remember whether to use {}s or ()s when building
 * responses.
 */
func SyncResponse(success bool, metadata interface{}) Response {
	return &syncResponse{success, metadata}
}

var EmptySyncResponse = &syncResponse{true, make(map[string]interface{})}

type asyncResponse struct {
	run    func() error
	cancel func() error
}

func (r *asyncResponse) Render(w http.ResponseWriter) error {
	op, err := CreateOperation(nil, r.run, r.cancel)
	if err != nil {
		return err
	}
	err = StartOperation(op)
	if err != nil {
		return err
	}

	return json.NewEncoder(w).Encode(lxd.Jmap{"type": lxd.Async, "operation": op})
}

func AsyncResponse(run func() error, cancel func() error) Response {
	return &asyncResponse{run, cancel}
}

type ErrorResponse struct {
	code int
	msg  string
}

func (r *ErrorResponse) Render(w http.ResponseWriter) error {
	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(lxd.Jmap{"type": lxd.Error, "error": r.msg, "error_code": r.code})

	if err != nil {
		return err
	}

	http.Error(w, buf.String(), r.code)
	return nil
}

/* Some standard responses */
var NotImplemented = &ErrorResponse{501, "not implemented"}
var NotFound = &ErrorResponse{404, "not found"}
var Forbidden = &ErrorResponse{403, "not authorized"}

func BadRequest(err error) Response {
	return &ErrorResponse{400, err.Error()}
}

func InternalError(err error) Response {
	return &ErrorResponse{500, err.Error()}
}

/*
 * Write the right error message based on err.
 */
func SmartError(err error) Response {
	switch err {
	case os.ErrNotExist:
		return NotFound
	case os.ErrPermission:
		return Forbidden
	default:
		return InternalError(err)
	}
}
