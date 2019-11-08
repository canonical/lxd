package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

func RestServer(tlsConfig *tls.Config) *http.Server {
	mux := mux.NewRouter()
	mux.StrictSlash(false)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response.SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		createCmd(mux, "1.0", c)
	}

	return &http.Server{Handler: mux, TLSConfig: tlsConfig}
}

func createCmd(restAPI *mux.Router, version string, c APIEndpoint) {
	var uri string
	if c.Path == "" {
		uri = fmt.Sprintf("/%s", version)
	} else {
		uri = fmt.Sprintf("/%s/%s", version, c.Path)
	}

	route := restAPI.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if !authenticate(r) {
			log.Println("Not authorized")
			response.InternalError(fmt.Errorf("Not authorized")).Render(w)
			return
		}

		// Dump full request JSON when in debug mode
		if debug && r.Method != "GET" && util.IsJSONRequest(r) {
			newBody := &bytes.Buffer{}
			captured := &bytes.Buffer{}
			multiW := io.MultiWriter(newBody, captured)
			if _, err := io.Copy(multiW, r.Body); err != nil {
				response.InternalError(err).Render(w)
				return
			}

			r.Body = shared.BytesReadCloser{Buf: newBody}
			shared.DebugJson(captured)
		}

		// Actually process the request
		var resp response.Response
		resp = response.NotImplemented(nil)

		handleRequest := func(action APIEndpointAction) response.Response {
			if action.Handler == nil {
				return response.NotImplemented(nil)
			}

			return action.Handler(r)
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
			resp = response.NotFound(fmt.Errorf("Method '%s' not found", r.Method))
		}

		// Handle errors
		err := resp.Render(w)
		if err != nil {
			err := response.InternalError(err).Render(w)
			if err != nil {
				logger.Errorf("Failed writing error for error, giving up")
			}
		}
	})

	// If the endpoint has a canonical name then record it so it can be used to build URLS
	// and accessed in the context of the request by the handler function.
	if c.Name != "" {
		route.Name(c.Name)
	}
}

func authenticate(r *http.Request) bool {
	clientCerts := map[string]x509.Certificate{"0": *cert}

	for _, cert := range r.TLS.PeerCertificates {
		trusted, _ := util.CheckTrustState(*cert, clientCerts)
		if trusted {
			return true
		}
	}

	return false
}
