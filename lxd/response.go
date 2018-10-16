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

	dqlite "github.com/CanonicalLtd/go-dqlite"
	"github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

type Response interface {
	Render(w http.ResponseWriter) error
	String() string
}

// Sync response
type syncResponse struct {
	success  bool
	etag     interface{}
	metadata interface{}
	location string
	code     int
	headers  map[string]string
}

func (r *syncResponse) Render(w http.ResponseWriter) error {
	// Set an appropriate ETag header
	if r.etag != nil {
		etag, err := util.EtagHash(r.etag)
		if err == nil {
			w.Header().Set("ETag", etag)
		}
	}

	// Prepare the JSON response
	status := api.Success
	if !r.success {
		status = api.Failure
	}

	if r.headers != nil {
		for h, v := range r.headers {
			w.Header().Set(h, v)
		}
	}

	if r.location != "" {
		w.Header().Set("Location", r.location)
		code := r.code
		if code == 0 {
			code = 201
		}
		w.WriteHeader(code)
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

func SyncResponseETag(success bool, metadata interface{}, etag interface{}) Response {
	return &syncResponse{success: success, metadata: metadata, etag: etag}
}

func SyncResponseLocation(success bool, metadata interface{}, location string) Response {
	return &syncResponse{success: success, metadata: metadata, location: location}
}

func SyncResponseRedirect(address string) Response {
	return &syncResponse{success: true, location: address, code: http.StatusPermanentRedirect}
}

func SyncResponseHeaders(success bool, metadata interface{}, headers map[string]string) Response {
	return &syncResponse{success: success, metadata: metadata, headers: headers}
}

var EmptySyncResponse = &syncResponse{success: true, metadata: make(map[string]interface{})}

type forwardedResponse struct {
	client  lxd.ContainerServer
	request *http.Request
}

func (r *forwardedResponse) Render(w http.ResponseWriter) error {
	info, err := r.client.GetConnectionInfo()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s%s", info.Addresses[0], r.request.URL.RequestURI())
	forwarded, err := http.NewRequest(r.request.Method, url, r.request.Body)
	if err != nil {
		return err
	}
	for key := range r.request.Header {
		forwarded.Header.Set(key, r.request.Header.Get(key))
	}

	httpClient, err := r.client.GetHTTPClient()
	if err != nil {
		return err
	}
	response, err := httpClient.Do(forwarded)
	if err != nil {
		return err
	}

	for key := range response.Header {
		w.Header().Set(key, response.Header.Get(key))
	}

	w.WriteHeader(response.StatusCode)
	_, err = io.Copy(w, response.Body)
	return err
}

func (r *forwardedResponse) String() string {
	return fmt.Sprintf("request to %s", r.request.URL)
}

// ForwardedResponse takes a request directed to a node and forwards it to
// another node, writing back the response it gegs.
func ForwardedResponse(client lxd.ContainerServer, request *http.Request) Response {
	return &forwardedResponse{
		client:  client,
		request: request,
	}
}

// ForwardedResponseIfTargetIsRemote redirects a request to the request has a
// targetNode parameter pointing to a node which is not the local one.
func ForwardedResponseIfTargetIsRemote(d *Daemon, request *http.Request) Response {
	targetNode := queryParam(request, "target")
	if targetNode == "" {
		return nil
	}

	// Figure out the address of the target node (which is possibly
	// this very same node).
	address, err := cluster.ResolveTarget(d.cluster, targetNode)
	if err != nil {
		return SmartError(err)
	}

	if address != "" {
		// Forward the response.
		cert := d.endpoints.NetworkCert()
		client, err := cluster.Connect(address, cert, false)
		if err != nil {
			return SmartError(err)
		}
		return ForwardedResponse(client, request)
	}

	return nil
}

// ForwardedResponseIfContainerIsRemote redirects a request to the node running
// the container with the given name. If the container is local, nothing gets
// done and nil is returned.
func ForwardedResponseIfContainerIsRemote(d *Daemon, r *http.Request, project, name string) (Response, error) {
	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfContainerIsRemote(d.cluster, project, name, cert)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	return ForwardedResponse(client, r), nil
}

// ForwardedResponseIfVolumeIsRemote redirects a request to the node hosting
// the volume with the given pool ID, name and type. If the container is local,
// nothing gets done and nil is returned. If more than one node has a matching
// volume, an error is returned.
//
// This is used when no targetNode is specified, and saves users some typing
// when the volume name/type is unique to a node.
func ForwardedResponseIfVolumeIsRemote(d *Daemon, r *http.Request, poolID int64, volumeName string, volumeType int) Response {
	if queryParam(r, "target") != "" {
		return nil
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfVolumeIsRemote(d.cluster, poolID, volumeName, volumeType, cert)
	if err != nil && err != db.ErrNoSuchObject {
		return SmartError(err)
	}
	if client == nil {
		return nil
	}
	return ForwardedResponse(client, r)
}

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

// Forwarded operation response.
//
// Returned when the operation has been created on another node
type forwardedOperationResponse struct {
	op      *api.Operation
	project string
}

func (r *forwardedOperationResponse) Render(w http.ResponseWriter) error {
	url := fmt.Sprintf("/%s/operations/%s", version.APIVersion, r.op.ID)
	if r.project != "" {
		url += fmt.Sprintf("?project=%s", r.project)
	}

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   r.op,
	}

	w.Header().Set("Location", url)
	w.WriteHeader(202)

	return util.WriteJSON(w, body, debug)
}

func (r *forwardedOperationResponse) String() string {
	return r.op.ID
}

// ForwardedOperationResponse creates a response that forwards the metadata of
// an operation created on another node.
func ForwardedOperationResponse(project string, op *api.Operation) Response {
	return &forwardedOperationResponse{
		op:      op,
		project: project,
	}
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

func NotImplemented(err error) Response {
	message := "not implemented"
	if err != nil {
		message = err.Error()
	}
	return &errorResponse{http.StatusNotImplemented, message}
}

func NotFound(err error) Response {
	message := "not found"
	if err != nil {
		message = err.Error()
	}
	return &errorResponse{http.StatusNotFound, message}
}

func Forbidden(err error) Response {
	message := "not authorized"
	if err != nil {
		message = err.Error()
	}
	return &errorResponse{http.StatusForbidden, message}
}

func Conflict(err error) Response {
	message := "already exists"
	if err != nil {
		message = err.Error()
	}
	return &errorResponse{http.StatusConflict, message}
}

func Unavailable(err error) Response {
	message := "unavailable"
	if err != nil {
		message = err.Error()
	}
	return &errorResponse{http.StatusServiceUnavailable, message}
}

func BadRequest(err error) Response {
	return &errorResponse{http.StatusBadRequest, err.Error()}
}

func InternalError(err error) Response {
	return &errorResponse{http.StatusInternalServerError, err.Error()}
}

func PreconditionFailed(err error) Response {
	return &errorResponse{http.StatusPreconditionFailed, err.Error()}
}

/*
 * SmartError returns the right error message based on err.
 */
func SmartError(err error) Response {
	switch errors.Cause(err) {
	case nil:
		return EmptySyncResponse
	case os.ErrNotExist:
		return NotFound(nil)
	case sql.ErrNoRows:
		return NotFound(nil)
	case db.ErrNoSuchObject:
		return NotFound(nil)
	case os.ErrPermission:
		return Forbidden(nil)
	case db.ErrAlreadyDefined:
		return Conflict(nil)
	case sqlite3.ErrConstraintUnique:
		return Conflict(nil)
	case dqlite.ErrNoAvailableLeader:
		return Unavailable(err)
	default:
		return InternalError(err)
	}
}
