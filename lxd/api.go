package main

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/auth/bearer"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

func restServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false) // Don't redirect to URL with trailing slash.
	mux.SkipClean(true)
	mux.UseEncodedPath() // Allow encoded values in path segments.

	for endpoint, f := range d.gateway.HandlerFuncs(d.heartbeatHandler, d.identityCache) {
		mux.HandleFunc(endpoint, f)
	}

	for _, c := range api10 {
		// Every 1.0 endpoint should have a type for the API metrics.
		if !slices.Contains(entity.APIMetricsEntityTypes(), c.MetricsType) {
			panic(`Endpoint "/1.0/` + c.Path + `" has invalid MetricsType: ` + string(c.MetricsType))
		}

		d.createCmd(mux, "1.0", c)
	}

	for _, c := range apiInternal {
		d.createCmd(mux, "internal", c)
	}

	for _, c := range apiRoot {
		d.createCmd(mux, "", c)
	}

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.TrackStartedRequest(r, entity.TypeServer) // Use TypeServer for not found handler
		logger.Info("Sending top level 404", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr})
		w.Header().Set("Content-Type", "application/json")
		_ = response.NotFound(nil).Render(w, r)
	})

	// Initialize API metrics with zero values.
	metrics.InitAPIMetrics()

	return &http.Server{
		Handler:           &lxdHTTPServer{r: mux, d: d},
		ConnContext:       request.SaveConnectionInContext,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 3 * time.Second,
	}
}

// isBrowserClient checks if the request is coming from a browser client.
func isBrowserClient(r *http.Request) bool {
	// Check if the User-Agent starts with "Mozilla" which is common for browsers.
	return strings.HasPrefix(r.Header.Get("User-Agent"), "Mozilla")
}

func metricsServer(d *Daemon) *http.Server {
	/* Setup the web server */
	mux := mux.NewRouter()
	mux.StrictSlash(false)
	mux.SkipClean(true)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = response.SyncResponse(true, []string{"/1.0"}).Render(w, r)
	})

	for endpoint, f := range d.gateway.HandlerFuncs(d.heartbeatHandler, d.identityCache) {
		mux.HandleFunc(endpoint, f)
	}

	d.createCmd(mux, "1.0", api10Cmd)
	d.createCmd(mux, "1.0", metricsCmd)

	mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.TrackStartedRequest(r, entity.TypeServer) // Use TypeServer for not found handler
		logger.Info("Sending top level 404", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr})
		w.Header().Set("Content-Type", "application/json")
		_ = response.NotFound(nil).Render(w, r)
	})

	return &http.Server{
		Handler:           &lxdHTTPServer{r: mux, d: d},
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}

type lxdHTTPServer struct {
	r *mux.Router
	d *Daemon
}

func (s *lxdHTTPServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
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

// handleUIAccessLink sets the session cookie if the request represents an initial UI  access link.
func handleUIAccessLink(w http.ResponseWriter, r *http.Request, clusterUUID string, identityCache *identity.Cache) error {
	isTokenRequest, location, token, subject := bearer.IsAPIRequest(r, clusterUUID)
	if !isTokenRequest || location != auth.TokenLocationQuery {
		// Do nothing if no token was sent, or if a token was set as a bearer token or cookie.
		return nil
	}

	// Authenticate the token. By specifying the location as "query", only the initial UI token secret
	// will be used to verify the token.
	requestorArgs, err := bearer.Authenticate(subject, token, auth.TokenLocationQuery, identityCache)
	if err != nil || requestorArgs == nil || requestorArgs.ExpiresAt == nil {
		// Just return a generic error because errors are shown via the UI instead.
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     bearer.CookieNameSession,
		Value:    token,
		Path:     "/",
		Secure:   true, // Only send the cookie over HTTPS.
		HttpOnly: true, // Do not allow JavaScript to access the cookie.
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(time.Until(*requestorArgs.ExpiresAt).Seconds()),
	})

	// Prevent caching of the response containing a session cookie.
	w.Header().Set("Cache-Control", "no-store")

	// Never send a Referer header when navigating away from this site
	// to avoid leaking URL / query params.
	w.Header().Set("Referrer-Policy", "no-referrer")

	return nil
}
