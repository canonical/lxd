package main

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/lxc/lxd"
)

type resp struct {
	Type     string      `json:"type"`
	Result   string      `json:"result"`
	Metadata interface{} `json:"metadata"`
}

func SyncResponse(success bool, metadata interface{}, w http.ResponseWriter) {
	result := "success"
	if !success {
		result = "failure"
	}

	r := resp{Type: string(lxd.Sync), Result: result, Metadata: metadata}
	enc, err := json.Marshal(&r)
	if err != nil {
		InternalError(w, err)
		return
	}
	lxd.Debugf(string(enc))

	w.Write(enc)
}

func EmptySyncResponse(w http.ResponseWriter) {
	SyncResponse(true, make(map[string]interface{}), w)
}

func AsyncResponse(run func() error, cancel func() error, w http.ResponseWriter) {
	op := CreateOperation(nil, run, cancel)
	err := StartOperation(op)
	if err != nil {
		InternalError(w, err)
		return
	}

	err = json.NewEncoder(w).Encode(lxd.Jmap{"type": lxd.Async, "operation": op})
	if err != nil {
		InternalError(w, err)
		return
	}
}

func ErrorResponse(code int, msg string, w http.ResponseWriter) {
	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(lxd.Jmap{"type": lxd.Error, "error": msg, "error_code": code})

	if err != nil {
		/* Can't use InternalError here */
		http.Error(w, "Error encoding error response!", 500)
		return
	}

	http.Error(w, buf.String(), code)
}

/* Some standard responses */
func NotImplemented(w http.ResponseWriter) {
	ErrorResponse(501, "not implemented", w)
}

func NotFound(w http.ResponseWriter) {
	ErrorResponse(404, "not found", w)
}

func Forbidden(w http.ResponseWriter) {
	ErrorResponse(403, "not authorized", w)
}

func BadRequest(w http.ResponseWriter, err error) {
	ErrorResponse(400, err.Error(), w)
}

func InternalError(w http.ResponseWriter, err error) {
	ErrorResponse(500, err.Error(), w)
}
