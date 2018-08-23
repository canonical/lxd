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
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type Response interface {
	Render(w http.ResponseWriter) error
	String() string
}

// Sync response
type syncResponse struct {
	success  bool
	metadata interface{}
	location string
}

func (r *syncResponse) Render(w http.ResponseWriter) error {
	status := api.Success
	if !r.success {
		status = api.Failure
	}

	if r.location != "" {
		w.Header().Set("Location", r.location)
		w.WriteHeader(201)
	}

	resp := api.ResponseRaw{
		Type:       api.SyncResponse,
		Status:     status.String(),
		StatusCode: int(status),
		Metadata:   r.metadata,
	}

	return util.WriteJSON(w, resp, debug)
}

func (r *syncResponse) String() string {
	if r.success {
		return "success"
	}

	return "failure"
}

func SyncResponse(success bool, metadata interface{}) Response {
	return &syncResponse{success: success, metadata: metadata}
}

func SyncResponseLocation(success bool, metadata interface{}, location string) Response {
	return &syncResponse{success: success, metadata: metadata, location: location}
}

var EmptySyncResponse = &syncResponse{success: true, metadata: make(map[string]interface{})}

// File transfer response
type fileResponseEntry struct {
	identifier string
	path       string
	filename   string
	buffer     []byte /* either a path or a buffer must be provided */
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
		var rs io.ReadSeeker
		var mt time.Time
		var sz int64

		if r.files[0].path == "" {
			rs = bytes.NewReader(r.files[0].buffer)
			mt = time.Now()
			sz = int64(len(r.files[0].buffer))
		} else {
			f, err := os.Open(r.files[0].path)
			if err != nil {
				return err
			}
			defer f.Close()

			fi, err := f.Stat()
			if err != nil {
				return err
			}

			mt = fi.ModTime()
			sz = fi.Size()
			rs = f
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", sz))
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline;filename=%s", r.files[0].filename))

		http.ServeContent(w, r.req, r.files[0].filename, mt, rs)
		if r.files[0].path != "" && r.removeAfterServe {
			err := os.Remove(r.files[0].path)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Now the complex multipart answer
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	for _, entry := range r.files {
		var rd io.Reader
		if entry.path != "" {
			fd, err := os.Open(entry.path)
			if err != nil {
				return err
			}
			defer fd.Close()
			rd = fd
		} else {
			rd = bytes.NewReader(entry.buffer)
		}

		fw, err := mw.CreateFormFile(entry.identifier, entry.filename)
		if err != nil {
			return err
		}

		_, err = io.Copy(fw, rd)
		if err != nil {
			return err
		}
	}
	mw.Close()

	w.Header().Set("Content-Type", mw.FormDataContentType())
	w.Header().Set("Content-Length", fmt.Sprintf("%d", body.Len()))

	_, err := io.Copy(w, body)
	return err
}

func (r *fileResponse) String() string {
	return fmt.Sprintf("%d files", len(r.files))
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

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   md,
	}

	w.Header().Set("Location", url)
	w.WriteHeader(202)

	return util.WriteJSON(w, body, debug)
}

func (r *operationResponse) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

func OperationResponse(op *operation) Response {
	return &operationResponse{op}
}

// Error response
type errorResponse struct {
	code int
	msg  string
}

func (r *errorResponse) String() string {
	return r.msg
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

	err := json.NewEncoder(output).Encode(shared.Jmap{"type": api.ErrorResponse, "error": r.msg, "error_code": r.code})

	if err != nil {
		return err
	}

	if debug {
		shared.DebugJson(captured)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(r.code)
	fmt.Fprintln(w, buf.String())

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
	case db.NoSuchObjectError:
		return NotFound
	case os.ErrPermission:
		return Forbidden
	case db.DbErrAlreadyDefined:
		return Conflict
	case sqlite3.ErrConstraintUnique:
		return Conflict
	default:
		return InternalError(err)
	}
}
