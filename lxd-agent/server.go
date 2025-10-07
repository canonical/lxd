package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

func restServer(tlsConfig *tls.Config, cert *x509.Certificate, d *Daemon) *http.Server {
	router := http.NewServeMux()

	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = response.SyncResponse(true, []string{"/1.0"}).Render(w, r)
	})

	for _, c := range api10 {
		createCmd(router, "1.0", c, cert, d)
	}

	return &http.Server{Handler: router, TLSConfig: tlsConfig}
}

func createCmd(restAPI *http.ServeMux, version string, c APIEndpoint, cert *x509.Certificate, d *Daemon) {
	var uri string
	if c.Path == "" {
		uri = "/" + version
	} else {
		uri = "/" + version + "/" + c.Path
	}

	restAPI.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if !authenticate(r, cert) {
			logger.Error("Not authorized")
			_ = response.InternalError(errors.New("Not authorized")).Render(w, r)
			return
		}

		// Dump full request JSON when in debug mode
		if r.Method != "GET" && util.IsJSONRequest(r) {
			newBody := &bytes.Buffer{}
			captured := &bytes.Buffer{}
			multiW := io.MultiWriter(newBody, captured)
			_, err := io.Copy(multiW, r.Body)
			if err != nil {
				_ = response.InternalError(err).Render(w, r)
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
		err := resp.Render(w, r)
		if err != nil {
			writeErr := response.InternalError(err).Render(w, r)
			if writeErr != nil {
				logger.Error("Failed writing error for HTTP response", logger.Ctx{"url": uri, "err": err, "writeErr": writeErr})
			}
		}
	})
}

func authenticate(r *http.Request, cert *x509.Certificate) bool {
	clientCerts := map[string]x509.Certificate{"0": *cert}

	for _, cert := range r.TLS.PeerCertificates {
		trusted, _ := util.CheckMutualTLS(*cert, clientCerts)
		if trusted {
			return true
		}
	}

	return false
}
