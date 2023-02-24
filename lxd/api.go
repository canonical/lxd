package main

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/mux"

	clusterConfig "github.com/lxc/lxd/lxd/cluster/config"
	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/project"
	lxdRequest "github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// swagger:operation GET / server api_get
//
//	Get the supported API endpoints
//
//	Returns a list of supported API versions (URLs).
//
//	Internal API endpoints are not reported as those aren't versioned and
//	should only be used by LXD itself.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: ["/1.0"]
func restServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false) // Don't redirect to URL with trailing slash.
	mux.SkipClean(true)
	mux.UseEncodedPath() // Allow encoded values in path segments.

	uiPath := os.Getenv("LXD_UI")
	uiEnabled := uiPath != "" && shared.PathExists(uiPath)
	if uiEnabled {
		uiHttpDir := uiHttpDir{http.Dir(uiPath)}
		mux.PathPrefix("/ui/").Handler(http.StripPrefix("/ui/", http.FileServer(uiHttpDir)))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		ua := r.Header.Get("User-Agent")
		if uiEnabled && strings.Contains(ua, "Gecko") {
			// Web browser handling.
			http.Redirect(w, r, "/ui/", 301)
		} else {
			// Normal client handling.
			_ = response.SyncResponse(true, []string{"/1.0"}).Render(w)
		}
	})

	for endpoint, f := range d.gateway.HandlerFuncs(d.heartbeatHandler, d.getTrustedCertificates) {
		mux.HandleFunc(endpoint, f)
	}

	for _, c := range api10 {
		d.createCmd(mux, "1.0", c)

		// Create any alias endpoints using the same handlers as the parent endpoint but
		// with a different path and name (so the handler can differentiate being called via
		// a different endpoint) if it wants to.
		for _, alias := range c.Aliases {
			ac := c
			ac.Name = alias.Name
			ac.Path = alias.Path
			d.createCmd(mux, "1.0", ac)
		}
	}

	for _, c := range apiInternal {
		d.createCmd(mux, "internal", c)
	}

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr})
		w.Header().Set("Content-Type", "application/json")
		_ = response.NotFound(nil).Render(w)
	})

	return &http.Server{
		Handler:     &lxdHttpServer{r: mux, d: d},
		ConnContext: lxdRequest.SaveConnectionInContext,
	}
}

func hoistReqVM(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) response.Response, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		trusted, inst, err := authenticateAgentCert(d, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !trusted {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}

		resp := f(d, inst, w, r)
		_ = resp.Render(w)
	}
}

func vSockServer(d *Daemon) *http.Server {
	return &http.Server{Handler: devLxdAPI(d, hoistReqVM)}
}

func metricsServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)
	mux.SkipClean(true)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = response.SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for endpoint, f := range d.gateway.HandlerFuncs(d.heartbeatHandler, d.getTrustedCertificates) {
		mux.HandleFunc(endpoint, f)
	}

	d.createCmd(mux, "1.0", api10Cmd)
	d.createCmd(mux, "1.0", metricsCmd)

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr})
		w.Header().Set("Content-Type", "application/json")
		_ = response.NotFound(nil).Render(w)
	})

	return &http.Server{Handler: &lxdHttpServer{r: mux, d: d}}
}

type lxdHttpServer struct {
	r *mux.Router
	d *Daemon
}

func (s *lxdHttpServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.URL.Path, "/internal") {
		<-s.d.setupChan

		// Set CORS headers, unless this is an internal request.
		setCORSHeaders(rw, req, s.d.State().GlobalConfig)
	}

	// OPTIONS request don't need any further processing
	if req.Method == "OPTIONS" {
		return
	}

	// Call the original server
	s.r.ServeHTTP(rw, req)
}

func setCORSHeaders(rw http.ResponseWriter, req *http.Request, config *clusterConfig.Config) {
	allowedOrigin := config.HTTPSAllowedOrigin()
	origin := req.Header.Get("Origin")
	if allowedOrigin != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
	}

	allowedMethods := config.HTTPSAllowedMethods()
	if allowedMethods != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Methods", allowedMethods)
	}

	allowedHeaders := config.HTTPSAllowedHeaders()
	if allowedHeaders != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
	}

	allowedCredentials := config.HTTPSAllowedCredentials()
	if allowedCredentials {
		rw.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

// Return true if this an API request coming from a cluster node that is
// notifying us of some user-initiated API request that needs some action to be
// taken on this node as well.
func isClusterNotification(r *http.Request) bool {
	return r.Header.Get("User-Agent") == request.UserAgentNotifier
}

// projectParam returns the project query parameter from the given request or "default" if parameter is not set.
func projectParam(request *http.Request) string {
	projectParam := queryParam(request, "project")
	if projectParam == "" {
		projectParam = project.Default
	}

	return projectParam
}

// Extract the given query parameter directly from the URL, never from an
// encoded body.
func queryParam(request *http.Request, key string) string {
	var values url.Values
	var err error

	if request.URL != nil {
		values, err = url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			logger.Warnf("Failed to parse query string %q: %v", request.URL.RawQuery, err)
			return ""
		}
	}

	if values == nil {
		values = make(url.Values)
	}

	return values.Get(key)
}

type uiHttpDir struct {
	http.FileSystem
}

func (fs uiHttpDir) Open(name string) (http.File, error) {
	fsFile, err := fs.FileSystem.Open(name)
	if err != nil && os.IsNotExist(err) {
		return fs.FileSystem.Open("index.html")
	}

	return fsFile, err
}
