package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"

	"github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

type resp struct {
	Type       lxd.ResponseType  `json:"type"`
	Status     string            `json:"status"`
	StatusCode shared.StatusCode `json:"status_code"`
	Metadata   interface{}       `json:"metadata"`
	Operation  string            `json:"operation"`
}

type Response interface {
	Render(w http.ResponseWriter) error
}

// Sync response
type syncResponse struct {
	success  bool
	metadata interface{}
}

func (r *syncResponse) Render(w http.ResponseWriter) error {
	status := shared.Success
	if !r.success {
		status = shared.Failure
	}

	resp := resp{Type: lxd.Sync, Status: status.String(), StatusCode: status, Metadata: r.metadata}
	return WriteJSON(w, resp)
}

func SyncResponse(success bool, metadata interface{}) Response {
	return &syncResponse{success, metadata}
}

var EmptySyncResponse = &syncResponse{true, make(map[string]interface{})}

// File transfer response
type fileResponseEntry struct {
	identifier string
	path       string
	filename   string
}

type fileResponse struct {
	req              *http.Request
	files            []fileResponseEntry
	headers          map[string]string
	removeAfterServe bool
}

func (r *fileResponse) Render(w http.ResponseWriter) error {
	if r.headers != nil {
		for k, v := range r.headers {
			w.Header().Set(k, v)
		}
	}

	// No file, well, it's easy then
	if len(r.files) == 0 {
		return nil
	}

	// For a single file, return it inline
	if len(r.files) == 1 {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline;filename=%s", r.files[0].filename))

		f, err := os.Open(r.files[0].path)
		if err != nil {
			return err
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			return err
		}

		http.ServeContent(w, r.req, r.files[0].filename, fi.ModTime(), f)
		if r.removeAfterServe {
			os.Remove(r.files[0].filename)
		}

		return nil
	}

	// Now the complex multipart answer
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	for _, entry := range r.files {
		fd, err := os.Open(entry.path)
		if err != nil {
			return err
		}
		defer fd.Close()

		fw, err := mw.CreateFormFile(entry.identifier, entry.filename)
		if err != nil {
			return err
		}

		_, err = io.Copy(fw, fd)
		if err != nil {
			return err
		}
	}

	mw.Close()
	w.Header().Set("Content-Type", mw.FormDataContentType())
	_, err := io.Copy(w, body)
	return err
}

func FileResponse(r *http.Request, files []fileResponseEntry, headers map[string]string, removeAfterServe bool) Response {
	return &fileResponse{r, files, headers, removeAfterServe}
}

// Operation response
type operationResponse struct {
	op *operation
}

func (r *operationResponse) Render(w http.ResponseWriter) error {
	_, err := r.op.Run()
	if err != nil {
		return err
	}

	url, md, err := r.op.Render()
	if err != nil {
		return err
	}

	body := resp{
		Type:       lxd.Async,
		Status:     shared.OperationCreated.String(),
		StatusCode: shared.OperationCreated,
		Operation:  url,
		Metadata:   md}

	w.Header().Set("Location", url)
	w.WriteHeader(202)

	return WriteJSON(w, body)
}

func OperationResponse(op *operation) Response {
	return &operationResponse{op}
}

// Error response
type errorResponse struct {
	code int
	msg  string
}

func (r *errorResponse) Render(w http.ResponseWriter) error {
	var output io.Writer

	buf := &bytes.Buffer{}
	output = buf
	var captured *bytes.Buffer
	if debug {
		captured = &bytes.Buffer{}
		output = io.MultiWriter(buf, captured)
	}

	err := json.NewEncoder(output).Encode(shared.Jmap{"type": lxd.Error, "error": r.msg, "error_code": r.code})

	if err != nil {
		return err
	}

	if debug {
		shared.DebugJson(captured)
	}
	http.Error(w, buf.String(), r.code)
	return nil
}

/* Some standard responses */
var NotImplemented = &errorResponse{http.StatusNotImplemented, "not implemented"}
var NotFound = &errorResponse{http.StatusNotFound, "not found"}
var Forbidden = &errorResponse{http.StatusForbidden, "not authorized"}
var Conflict = &errorResponse{http.StatusConflict, "already exists"}

func BadRequest(err error) Response {
	return &errorResponse{http.StatusBadRequest, err.Error()}
}

func InternalError(err error) Response {
	return &errorResponse{http.StatusInternalServerError, err.Error()}
}

/*
 * SmartError returns the right error message based on err.
 */
func SmartError(err error) Response {
	switch err {
	case nil:
		return EmptySyncResponse
	case os.ErrNotExist:
		return NotFound
	case sql.ErrNoRows:
		return NotFound
	case NoSuchObjectError:
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
