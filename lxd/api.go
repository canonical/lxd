package main

import (
	"net/http"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared/logger"
)

// RestServer creates an http.Server capable of handling requests against the LXD REST
// API endpoint.
func RestServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		d.createCmd(mux, "1.0", c)
	}

	for _, c := range apiInternal {
		d.createCmd(mux, "internal", c)
	}

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", log.Ctx{"url": r.URL})
		w.Header().Set("Content-Type", "application/json")
		NotFound.Render(w)
	})

	return &http.Server{Handler: &lxdHttpServer{r: mux, d: d}}
}

type lxdHttpServer struct {
	r *mux.Router
	d *Daemon
}

func (s *lxdHttpServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	allowedOrigin := daemonConfig["core.https_allowed_origin"].Get()
	origin := req.Header.Get("Origin")
	if allowedOrigin != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
	}

	allowedMethods := daemonConfig["core.https_allowed_methods"].Get()
	if allowedMethods != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Methods", allowedMethods)
	}

	allowedHeaders := daemonConfig["core.https_allowed_headers"].Get()
	if allowedHeaders != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
	}

	allowedCredentials := daemonConfig["core.https_allowed_credentials"].GetBool()
	if allowedCredentials {
		rw.Header().Set("Access-Control-Allow-Credentials", "true")
	}

	// OPTIONS request don't need any further processing
	if req.Method == "OPTIONS" {
		return
	}

	// Call the original server
	s.r.ServeHTTP(rw, req)
}
