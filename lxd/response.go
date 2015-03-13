package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
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
  Used for returning images.
  fname: name of the file without path
  clength: sets content-size of response
  ctype: content-type, depends on compression
*/
type fileResponse struct {
	path     string
	filename string
	clength  int64
	ctype    string
}

func FileResponse(path string, filename string, size int64, ctype string) Response {
	return &fileResponse{path, filename, size, ctype}
}

func (r *fileResponse) Render(w http.ResponseWriter) error {

	w.Header().Set("Content-Type", r.ctype)
	w.Header().Set("Content-Length", strconv.FormatInt(r.clength, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline;filename=%s", r.filename))

	f, err := os.Open(r.path)
	defer f.Close()
	if err != nil {
		return err
	}

	_, err = io.Copy(w, f)
	return err

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
	run        func() shared.OperationResult
	cancel     func() error
	ws         shared.OperationWebsocket
	containers []string
	done       chan shared.OperationResult
}

func (r *asyncResponse) Render(w http.ResponseWriter) error {

	op, err := CreateOperation(nil, r.run, r.cancel, r.ws)
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
	}

	if r.containers != nil && len(r.containers) > 0 {
		body.Resources = map[string][]string{}
		var containers []string
		for _, c := range r.containers {
			containers = append(containers, fmt.Sprintf("/%s/containers/%s", shared.Version, c))
		}

		body.Resources["containers"] = containers
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
	case os.ErrPermission:
		return Forbidden
	default:
		return InternalError(err)
	}
}
