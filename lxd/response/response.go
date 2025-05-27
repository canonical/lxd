package response

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/tcp"
)

var debug bool

// Init sets the debug variable to the provided value and registers any additional smart error mappings.
func Init(d bool, smartErrors map[int][]error) {
	debug = d

	for code, additionalErrors := range smartErrors {
		existingErrs, ok := httpResponseErrors[code]
		if ok {
			httpResponseErrors[code] = append(existingErrs, additionalErrors...)
			continue
		}

		httpResponseErrors[code] = additionalErrors
	}
}

// Response represents an API response.
type Response interface {
	Render(w http.ResponseWriter, r *http.Request) error
	String() string
}

// devLXDResponse represents a devLXD API response.
type devLXDResponse struct {
	content     any
	code        int
	etag        string
	contentType string
	err         error
}

// Render renders a response for requests against the /dev/lxd socket.
func (r *devLXDResponse) Render(w http.ResponseWriter, req *http.Request) (err error) {
	// Handle response when interacting over vsock (LXD Agent running in a VM).
	// In such case, the response must be returned in api.Response format.
	isDevLXDOverVsock, _ := req.Context().Value(request.CtxDevLXDOverVsock).(bool)
	if isDevLXDOverVsock {
		if r.code != http.StatusOK {
			return SmartError(r.err).Render(w, req)
		}

		// Set ETag header if ETag is provided.
		if r.etag != "" {
			w.Header().Set("ETag", r.etag)
		}

		return SyncResponse(true, r.content).Render(w, req)
	}

	// From this point on, we are responding to a request over unix socket.
	if r.code != http.StatusOK {
		http.Error(w, r.err.Error(), r.code)
		return nil
	}

	// Set ETag header if ETag is provided.
	if r.etag != "" {
		w.Header().Set("ETag", r.etag)
	}

	if r.contentType == "json" {
		w.Header().Set("Content-Type", "application/json")

		var debugLogger logger.Logger
		if debug {
			debugLogger = logger.Logger(logger.Log)
		}

		return util.WriteJSON(w, r.content, debugLogger)
	}

	if r.contentType != "websocket" {
		w.Header().Set("Content-Type", "application/octet-stream")

		if r.content != nil {
			_, err = fmt.Fprint(w, fmt.Sprint(r.content))
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *devLXDResponse) String() string {
	if r.code == http.StatusOK {
		return "success"
	}

	return "failure"
}

// DevLXDErrorResponse returns an error response.
func DevLXDErrorResponse(err error) Response {
	code, ok := api.StatusErrorMatch(err)
	if !ok {
		code = http.StatusInternalServerError
	}

	return &devLXDResponse{
		code:        code,
		contentType: "raw",
		err:         err,
	}
}

// DevLXDResponse represents a devLXDResponse.
func DevLXDResponse(code int, content any, contentType string) Response {
	return &devLXDResponse{
		code:        code,
		content:     content,
		contentType: contentType,
	}
}

// DevLXDResponseETag returns a devLXDResponse with the provided ETag configured.
// If ETag is not empty, it will be set in the response headers.
func DevLXDResponseETag(code int, content any, contentType string, etag string) Response {
	return &devLXDResponse{
		code:        code,
		content:     content,
		contentType: contentType,
		etag:        etag,
	}
}

// Sync response.
type syncResponse struct {
	success   bool
	etag      any
	metadata  any
	location  string
	code      int
	headers   map[string]string
	plaintext bool
	compress  bool
}

// EmptySyncResponse represents an empty syncResponse.
var EmptySyncResponse = &syncResponse{success: true, metadata: make(map[string]any)}

// SyncResponse returns a new syncResponse with the success and metadata fields
// set to the provided values.
func SyncResponse(success bool, metadata any) Response {
	return &syncResponse{success: success, metadata: metadata}
}

// SyncResponseETag returns a new syncResponse with an etag.
func SyncResponseETag(success bool, metadata any, etag any) Response {
	return &syncResponse{success: success, metadata: metadata, etag: etag}
}

// SyncResponseLocation returns a new syncResponse with a location.
func SyncResponseLocation(success bool, metadata any, location string) Response {
	return &syncResponse{success: success, metadata: metadata, location: location}
}

// SyncResponseRedirect returns a new syncResponse with a location, indicating
// a permanent redirect.
func SyncResponseRedirect(address string) Response {
	return &syncResponse{success: true, location: address, code: http.StatusPermanentRedirect}
}

// SyncResponseHeaders returns a new syncResponse with headers.
func SyncResponseHeaders(success bool, metadata any, headers map[string]string) Response {
	return &syncResponse{success: success, metadata: metadata, headers: headers}
}

// SyncResponsePlain return a new syncResponse with plaintext.
func SyncResponsePlain(success bool, compress bool, metadata string) Response {
	return &syncResponse{success: success, metadata: metadata, plaintext: true, compress: compress}
}

// Render renders a synchronous response.
func (r *syncResponse) Render(w http.ResponseWriter, req *http.Request) error {
	// Set an appropriate ETag header
	if r.etag != nil {
		etag, err := util.EtagHash(r.etag)
		if err == nil {
			w.Header().Set("ETag", `"`+etag+`"`)
		}
	}

	if r.headers != nil {
		for h, v := range r.headers {
			w.Header().Set(h, v)
		}
	}

	code := r.code

	if r.location != "" {
		w.Header().Set("Location", r.location)
		if code == 0 {
			code = 201
		}
	}

	// Handle plain text headers.
	if r.plaintext {
		w.Header().Set("Content-Type", "text/plain")
	}

	// Handle compression.
	if r.compress {
		w.Header().Set("Content-Encoding", "gzip")
	}

	// Write header and status code.
	if code == 0 {
		code = http.StatusOK
	}

	if w.Header().Get("Connection") != "keep-alive" {
		w.WriteHeader(code)
	}

	// Prepare the JSON response
	status := api.Success
	if !r.success {
		status = api.Failure

		// If the metadata is an error, consider the response a SmartError
		// to propagate the data and preserve the status code.
		err, ok := r.metadata.(error)
		if ok {
			return SmartError(err).Render(w, req)
		}
	}

	// defer calling the callback function after possibly considering the response a SmartError.
	defer func() {
		if r.success {
			metrics.UseMetricsCallback(req, metrics.Success)
		} else {
			metrics.UseMetricsCallback(req, metrics.ErrorServer)
		}
	}()

	// Handle plain text responses.
	if r.plaintext {
		if r.metadata != nil {
			if r.compress {
				comp := gzip.NewWriter(w)
				defer comp.Close()

				_, err := comp.Write([]byte(r.metadata.(string)))
				if err != nil {
					return err
				}
			} else {
				_, err := w.Write([]byte(r.metadata.(string)))
				if err != nil {
					return err
				}
			}
		}

		return nil
	}

	// Handle JSON responses.
	resp := api.ResponseRaw{
		Type:       api.SyncResponse,
		Status:     status.String(),
		StatusCode: int(status),
		Metadata:   r.metadata,
	}

	var debugLogger logger.Logger
	if debug {
		debugLogger = logger.AddContext(logger.Ctx{"http_code": code})
	}

	return util.WriteJSON(w, resp, debugLogger)
}

func (r *syncResponse) String() string {
	if r.success {
		return "success"
	}

	return "failure"
}

// Error response.
type errorResponse struct {
	code int   // Code to return in both the HTTP header and Code field of the response body.
	err  error // Error whose string representation will be returned in the Error field of the response body.
}

// ErrorResponse returns an error response with the given code and msg.
func ErrorResponse(code int, msg string) Response {
	return &errorResponse{code, errors.New(msg)}
}

// BadRequest returns a bad request response (400) with the given error.
func BadRequest(err error) Response {
	return &errorResponse{code: http.StatusBadRequest, err: err}
}

// Conflict returns a conflict response (409) with the given error.
func Conflict(err error) Response {
	return &errorResponse{code: http.StatusConflict, err: err}
}

// Forbidden returns a forbidden response (403) with the given error.
func Forbidden(err error) Response {
	return &errorResponse{code: http.StatusForbidden, err: err}
}

// InternalError returns an internal error response (500) with the given error.
func InternalError(err error) Response {
	return &errorResponse{code: http.StatusInternalServerError, err: err}
}

// NotFound returns a not found response (404) with the given error.
func NotFound(err error) Response {
	return &errorResponse{code: http.StatusNotFound, err: err}
}

// NotImplemented returns a not implemented response (501) with the given error.
func NotImplemented(err error) Response {
	return &errorResponse{code: http.StatusNotImplemented, err: err}
}

// PreconditionFailed returns a precondition failed response (412) with the
// given error.
func PreconditionFailed(err error) Response {
	return &errorResponse{code: http.StatusPreconditionFailed, err: err}
}

// Unavailable return an unavailable response (503) with the given error.
func Unavailable(err error) Response {
	return &errorResponse{code: http.StatusServiceUnavailable, err: err}
}

// Unauthorized return an unauthorized response (401) with the given error.
func Unauthorized(err error) Response {
	return &errorResponse{code: http.StatusUnauthorized, err: err}
}

func (r *errorResponse) String() string {
	if r.err != nil {
		return r.err.Error()
	}

	return http.StatusText(r.code)
}

// Render renders a response that indicates an error on the request handling.
func (r *errorResponse) Render(w http.ResponseWriter, req *http.Request) error {
	var output io.Writer

	buf := &bytes.Buffer{}
	output = buf
	var captured *bytes.Buffer
	if debug {
		captured = &bytes.Buffer{}
		output = io.MultiWriter(buf, captured)
	}

	resp := api.ResponseRaw{
		Type:  api.ErrorResponse,
		Error: r.String(),
		Code:  r.code, // Set the error code in the Code field of the response body.
	}

	defer func() {
		// Use the callback function to count the request for the API metrics.
		if r.code >= 400 && r.code < 500 {
			// 4* codes are considered client errors on HTTP.
			metrics.UseMetricsCallback(req, metrics.ErrorClient)
		} else {
			// Any other status code here shoud be higher than or equal to 500 and is considered a server error.
			metrics.UseMetricsCallback(req, metrics.ErrorServer)
		}
	}()

	err := json.NewEncoder(output).Encode(resp)

	if err != nil {
		return err
	}

	if debug {
		debugLogger := logger.AddContext(logger.Ctx{"http_code": r.code})
		util.DebugJSON("Error Response", captured, debugLogger)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if w.Header().Get("Connection") != "keep-alive" {
		w.WriteHeader(r.code) // Set the error code in the HTTP header response.
	}

	_, err = fmt.Fprintln(w, buf.String())

	return err
}

// FileResponseEntry represents a file response entry.
type FileResponseEntry struct {
	// Required.
	Identifier string
	Filename   string

	// Read from a filesystem path.
	Path string

	// Read from a file.
	File         io.ReadSeeker
	FileSize     int64
	FileModified time.Time

	// Optional.
	Cleanup func()
}

type fileResponse struct {
	files   []FileResponseEntry
	headers map[string]string
}

// FileResponse returns a new file response.
func FileResponse(files []FileResponseEntry, headers map[string]string) Response {
	return &fileResponse{files, headers}
}

// Render renders a file response.
func (r *fileResponse) Render(w http.ResponseWriter, req *http.Request) error {
	if r.headers != nil {
		for k, v := range r.headers {
			w.Header().Set(k, v)
		}
	}

	var err error
	defer func() {
		if err == nil {
			// If there was an error on Render, the callback function will be called during the error handling.
			metrics.UseMetricsCallback(req, metrics.Success)
		}
	}()

	// No file, well, it's easy then
	if len(r.files) == 0 {
		return nil
	}

	// For a single file, return it inline
	if len(r.files) == 1 {
		remoteConn := ucred.GetConnFromContext(req.Context())
		remoteTCP, err := tcp.ExtractConn(remoteConn)
		if err == nil && remoteTCP != nil {
			// Apply TCP timeouts if remote connection is TCP (rather than Unix).
			err = tcp.SetTimeouts(remoteTCP, 10*time.Second)
			if err != nil {
				return api.StatusErrorf(http.StatusInternalServerError, "Failed setting TCP timeouts on remote connection: %w", err)
			}
		}

		var rs io.ReadSeeker
		var mt time.Time
		var sz int64

		if r.files[0].Cleanup != nil {
			defer r.files[0].Cleanup()
		}

		if r.files[0].File != nil {
			rs = r.files[0].File
			mt = r.files[0].FileModified
			sz = r.files[0].FileSize
		} else {
			var f *os.File
			f, err = os.Open(r.files[0].Path)
			if err != nil {
				return err
			}

			defer func() { _ = f.Close() }()

			var fi fs.FileInfo
			fi, err = f.Stat()
			if err != nil {
				return err
			}

			mt = fi.ModTime()
			sz = fi.Size()
			rs = f
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(sz, 10))
		w.Header().Set("Content-Disposition", "inline;filename="+r.files[0].Filename)

		http.ServeContent(w, req, r.files[0].Filename, mt, rs)

		return nil
	}

	// Now the complex multipart answer.
	mw := multipart.NewWriter(w)
	defer func() { _ = mw.Close() }()

	w.Header().Set("Content-Type", mw.FormDataContentType())
	w.Header().Set("Transfer-Encoding", "chunked")

	for _, entry := range r.files {
		var rd io.Reader
		if entry.File != nil {
			rd = entry.File
		} else {
			var fd *os.File
			fd, err = os.Open(entry.Path)
			if err != nil {
				return err
			}

			defer func() { _ = fd.Close() }()

			rd = fd
		}

		var fw io.Writer
		fw, err = mw.CreateFormFile(entry.Identifier, entry.Filename)
		if err != nil {
			return err
		}

		_, err := io.Copy(fw, rd)
		if err != nil {
			return err
		}

		if entry.Cleanup != nil {
			entry.Cleanup()
		}
	}

	err = mw.Close()

	return err
}

func (r *fileResponse) String() string {
	return strconv.FormatInt(int64(len(r.files)), 10) + " files"
}

type forwardedResponse struct {
	client lxd.InstanceServer
}

// ForwardedResponse takes a request directed to a node and forwards it to
// another node, writing back the response it gegs.
func ForwardedResponse(client lxd.InstanceServer) Response {
	return &forwardedResponse{
		client: client,
	}
}

// Render renders a response for a forwarded request.
func (r *forwardedResponse) Render(w http.ResponseWriter, req *http.Request) error {
	info, err := r.client.GetConnectionInfo()
	if err != nil {
		return err
	}

	url := info.Addresses[0] + req.URL.RequestURI()
	forwarded, err := http.NewRequest(req.Method, url, req.Body)
	if err != nil {
		return err
	}

	for key := range req.Header {
		forwarded.Header.Set(key, req.Header.Get(key))
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

	if w.Header().Get("Connection") != "keep-alive" {
		w.WriteHeader(response.StatusCode)
	}

	_, err = io.Copy(w, response.Body)
	return err
}

func (r *forwardedResponse) String() string {
	return "forwarded response"
}

type manualResponse struct {
	hook func(w http.ResponseWriter) error
}

// ManualResponse creates a new manual response responder.
func ManualResponse(hook func(w http.ResponseWriter) error) Response {
	return &manualResponse{hook: hook}
}

// Render renders a manual response.
func (r *manualResponse) Render(w http.ResponseWriter, req *http.Request) error {
	err := r.hook(w)

	if err == nil {
		// If there was an error on Render, the callback function will be called during the error handling.
		metrics.UseMetricsCallback(req, metrics.Success)
	}

	return err
}

func (r *manualResponse) String() string {
	return "unknown"
}
