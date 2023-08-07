package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// restServer creates an HTTP server with the provided TLS configuration and handles API routes for version 1.0.
func restServer(tlsConfig *tls.Config, cert *x509.Certificate, debug bool, d *Daemon) *http.Server {
	mux := mux.NewRouter()
	mux.StrictSlash(false) // Don't redirect to URL with trailing slash.
	mux.UseEncodedPath()   // Allow encoded values in path segments.

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = response.SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		createCmd(mux, "1.0", c, cert, debug, d)
	}

	return &http.Server{Handler: mux, TLSConfig: tlsConfig}
}

// createCmd sets up an API endpoint on the given Router with the specified version, handler functions, and authentication.
func createCmd(restAPI *mux.Router, version string, c APIEndpoint, cert *x509.Certificate, debug bool, d *Daemon) {
	var uri string
	if c.Path == "" {
		uri = fmt.Sprintf("/%s", version)
	} else {
		uri = fmt.Sprintf("/%s/%s", version, c.Path)
	}

	route := restAPI.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if !authenticate(r, cert) {
			logger.Error("Not authorized")
			_ = response.InternalError(fmt.Errorf("Not authorized")).Render(w)
			return
		}

		// Dump full request JSON when in debug mode
		if r.Method != "GET" && util.IsJSONRequest(r) {
			newBody := &bytes.Buffer{}
			captured := &bytes.Buffer{}
			multiW := io.MultiWriter(newBody, captured)
			_, err := io.Copy(multiW, r.Body)
			if err != nil {
				_ = response.InternalError(err).Render(w)
				return
			}

			r.Body = shared.BytesReadCloser{Buf: newBody}
			util.DebugJSON("API Request", captured, logger.Log)
		}

		// Actually process the request
		var resp response.Response

		handleRequest := func(action APIEndpointAction) response.Response {
			if action.Handler == nil {
				return response.NotImplemented(nil)
			}

			return action.Handler(d, r)
		}

		switch r.Method {
		case "GET":
			resp = handleRequest(c.Get)
		case "PUT":
			resp = handleRequest(c.Put)
		case "POST":
			resp = handleRequest(c.Post)
		case "DELETE":
			resp = handleRequest(c.Delete)
		case "PATCH":
			resp = handleRequest(c.Patch)
		default:
			resp = response.NotFound(fmt.Errorf("Method %q not found", r.Method))
		}

		// Handle errors
		err := resp.Render(w)
		if err != nil {
			writeErr := response.InternalError(err).Render(w)
			if writeErr != nil {
				logger.Error("Failed writing error for HTTP response", logger.Ctx{"url": uri, "error": err, "writeErr": writeErr})
			}
		}
	})

	// If the endpoint has a canonical name then record it so it can be used to build URLS
	// and accessed in the context of the request by the handler function.
	if c.Name != "" {
		route.Name(c.Name)
	}
}

// authenticate checks if the request is authenticated based on the client's TLS certificate.
func authenticate(r *http.Request, cert *x509.Certificate) bool {
	clientCerts := map[string]x509.Certificate{"0": *cert}

	for _, cert := range r.TLS.PeerCertificates {
		trusted, _ := util.CheckTrustState(*cert, clientCerts, nil, false)
		if trusted {
			return true
		}
	}

	return false
}
