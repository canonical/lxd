package main

import (
	"net/http"
	"net/url"
	"strings"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/project"
	lxdRequest "github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared/logger"
)

// swagger:operation GET / server api_get
//
// Get the supported API enpoints
//
// Returns a list of supported API versions (URLs).
//
// Internal API endpoints are not reported as those aren't versioned and
// should only be used by LXD itself.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: ["/1.0"]
func restServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)
	mux.SkipClean(true)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response.SyncResponse(true, []string{"/1.0"}).Render(w)
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
		logger.Info("Sending top level 404", log.Ctx{"url": r.URL})
		w.Header().Set("Content-Type", "application/json")
		response.NotFound(nil).Render(w)
	})

	return &http.Server{
		Handler:     &lxdHttpServer{r: mux, d: d},
		ConnContext: lxdRequest.SaveConnectionInContext,
	}
}

type lxdHttpServer struct {
	r *mux.Router
	d *Daemon
}

func (s *lxdHttpServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Set CORS headers, unless this is an internal request.
	if !strings.HasPrefix(req.URL.Path, "/internal") {
		<-s.d.setupChan
		err := s.d.cluster.Transaction(func(tx *db.ClusterTx) error {
			config, err := cluster.ConfigLoad(tx)
			if err != nil {
				return err
			}
			setCORSHeaders(rw, req, config)
			return nil
		})
		if err != nil {
			resp := response.SmartError(err)
			resp.Render(rw)
			return
		}
	}

	// OPTIONS request don't need any further processing
	if req.Method == "OPTIONS" {
		return
	}

	// Call the original server
	s.r.ServeHTTP(rw, req)
}

func setCORSHeaders(rw http.ResponseWriter, req *http.Request, config *cluster.Config) {
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
