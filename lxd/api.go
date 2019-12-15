package main

import (
	"net/http"
	"net/url"
	"strings"
	"reflect"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/api"
)

// RestServer creates an http.Server capable of handling requests against the LXD REST
// API endpoint.
func RestServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response.SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for endpoint, f := range d.gateway.HandlerFuncs(d.NodeRefreshTask) {
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

	return &http.Server{Handler: &lxdHttpServer{r: mux, d: d}}
}

type lxdHttpServer struct {
	r *mux.Router
	d *Daemon
}

func (s *lxdHttpServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// Set CORS headers, unless this is an internal or gRPC request.
	if !strings.HasPrefix(req.URL.Path, "/internal") && !strings.HasPrefix(req.URL.Path, "/protocol.SQL") {
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
	return r.Header.Get("User-Agent") == "lxd-cluster-notifier"
}

// Extract the project query parameter from the given request.
func projectParam(request *http.Request) string {
	project := queryParam(request, "project")
	if project == "" {
		project = "default"
	}
	return project
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

func doFilter (fstr string, result []interface{}) []interface{} {
	newResult := result[:0]
	for _,obj := range result {
		if applyFilter(fstr, obj) {
			newResult = append(newResult, obj)
		}
	}
	return newResult
}

func applyFilter (fstr string, obj interface{}) bool {
	filterSplit := strings.Fields(fstr)

	index := 0
	result := true
	prevLogical := "and"
	not := false

	queryLen := len(filterSplit)

	for index < queryLen {
		if strings.EqualFold(filterSplit[index], "not") {
			not = true
			index++
		}
		field := filterSplit[index]
		operator := filterSplit[index+1]
		value := filterSplit[index+2]
		index+=3
		// eval 

		curResult := false
		
		logger.Warnf("JackieError: %s", reflect.TypeOf(obj))
		objType := reflect.TypeOf(obj).String()
		switch (objType) {
			case "*api.Instance":
				curResult = evaluateFieldInstance(field, value, operator, obj.(*api.Instance))
				break
			case "*api.InstanceFull":
				curResult = evaluateFieldInstanceFull(field, value, operator, obj.(*api.InstanceFull))
				break
			case "*api.Image":
				curResult = evaluateFieldImage(field, value, operator, obj.(*api.Image))
				break
			default:
				logger.Warnf("Unable to identify type")
				break

		}

		if not {
			not = false
			curResult = !curResult
		}

		if strings.EqualFold (prevLogical, "and") {
			result = curResult && result
		} else {
			if strings.EqualFold(prevLogical, "or") {
				result = curResult || result
			}
		}

		if index < queryLen {
			prevLogical = filterSplit[index]
			index++
		}
	}

	return result
}
