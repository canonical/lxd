package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/stgraber/lxd-go-sqlite3"
)

type resp struct {
	Type       lxd.ResponseType       `json:"type"`
	Status     string                 `json:"status"`
	StatusCode shared.OperationStatus `json:"status_code"`
	Metadata   interface{}            `json:"metadata"`
}

type Response interface {
	Render(w http.ResponseWriter) error
}

type syncResponse struct {
	success  bool
	metadata interface{}
}

/*
  fname: name of the file without path
  headers: any other headers that should be set in the response
*/
type fileResponse struct {
	req      *http.Request
	path     string
	filename string
	headers  map[string]string
}

func FileResponse(r *http.Request, path string, filename string, headers map[string]string) Response {
	return &fileResponse{r, path, filename, headers}
}

func (r *fileResponse) Render(w http.ResponseWriter) error {

	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	if r.headers != nil {
		for k, v := range r.headers {
			w.Header().Set(k, v)
		}
	}

	http.ServeContent(w, r.req, r.filename, fi.ModTime(), f)
	return nil
}

func (r *syncResponse) Render(w http.ResponseWriter) error {
	status := shared.Success
	if !r.success {
		status = shared.Failure
	}

	resp := resp{Type: lxd.Sync, Status: status.String(), StatusCode: status, Metadata: r.metadata}
	enc, err := json.Marshal(&resp)
	if err != nil {
		return err
	}

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

type async struct {
	Type       lxd.ResponseType       `json:"type"`
	Status     string                 `json:"status"`
	StatusCode shared.OperationStatus `json:"status_code"`
	Operation  string                 `json:"operation"`
	Resources  map[string][]string    `json:"resources"`
	Metadata   interface{}            `json:"metadata"`
}

type asyncResponse struct {
	run       func() shared.OperationResult
	cancel    func() error
	ws        shared.OperationWebsocket
	resources map[string][]string
	metadata  shared.Jmap
	done      chan shared.OperationResult
}

func (r *asyncResponse) Render(w http.ResponseWriter) error {

	op, err := CreateOperation(r.metadata, r.resources, r.run, r.cancel, r.ws)
	if err != nil {
		return err
	}

	err = StartOperation(op)
	if err != nil {
		return err
	}

	body := async{Type: lxd.Async, Status: shared.OK.String(), StatusCode: shared.OK, Operation: op}
	if r.ws != nil {
		body.Metadata = r.ws.Metadata()
	} else if r.metadata != nil {
		body.Metadata = r.metadata
	}

	if r.resources != nil {
		resources := make(map[string][]string)
		for key, value := range r.resources {
			var values []string
			for _, c := range value {
				values = append(values, fmt.Sprintf("/%s/%s/%s", shared.APIVersion, key, c))
			}
			resources[key] = values
		}
		body.Resources = resources
	}

	w.Header().Set("Location", op)
	w.WriteHeader(202)
	return json.NewEncoder(w).Encode(body)
}

func AsyncResponse(run func() shared.OperationResult, cancel func() error) Response {
	return &asyncResponse{run: run, cancel: cancel}
}

func AsyncResponseWithWs(ws shared.OperationWebsocket, cancel func() error) Response {
	return &asyncResponse{run: ws.Do, cancel: cancel, ws: ws}
}

type ErrorResponse struct {
	code int
	msg  string
}

func (r *ErrorResponse) Render(w http.ResponseWriter) error {
	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(shared.Jmap{"type": lxd.Error, "error": r.msg, "error_code": r.code})

	if err != nil {
		return err
	}

	http.Error(w, buf.String(), r.code)
	return nil
}

/* Some standard responses */
var NotImplemented = &ErrorResponse{http.StatusNotImplemented, "not implemented"}
var NotFound = &ErrorResponse{http.StatusNotFound, "not found"}
var Forbidden = &ErrorResponse{http.StatusForbidden, "not authorized"}
var Conflict = &ErrorResponse{http.StatusConflict, "already exists"}

func BadRequest(err error) Response {
	return &ErrorResponse{http.StatusBadRequest, err.Error()}
}

func InternalError(err error) Response {
	return &ErrorResponse{http.StatusInternalServerError, err.Error()}
}

/*
 * Write the right error message based on err.
 */
func SmartError(err error) Response {
	switch err {
	case os.ErrNotExist:
		return NotFound
	case sql.ErrNoRows:
		return NotFound
	case NoSuchImageError:
		return NotFound
	case os.ErrPermission:
		return Forbidden
	case DbErrAlreadyDefined:
		return Conflict
	case sqlite3.ErrConstraintUnique:
		return Conflict
	default:
		return InternalError(err)
	}
}
