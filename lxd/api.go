package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

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
	mux := &lxdMux{ServeMux: http.NewServeMux()}

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

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
	mux := &lxdMux{ServeMux: http.NewServeMux()}

	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = response.SyncResponse(true, []string{"/1.0"}).Render(w, r)
	})

	for endpoint, f := range d.gateway.HandlerFuncs(d.heartbeatHandler, d.identityCache) {
		mux.HandleFunc(endpoint, f)
	}

	d.createCmd(mux, "1.0", api10Cmd)
	d.createCmd(mux, "1.0", metricsCmd)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

type lxdMux struct {
	*http.ServeMux
	prefixHandlers []lxdPrefixHandler
}

// lxdPrefixHandler handles patterns that could not be registered on ServeMux
// due to routing conflicts [images/aliases/{name} vs. "images/{fingerprint}/export"]
// It matches requests by prefix, an optional suffix, and a single-segment variable in between.
type lxdPrefixHandler struct {
	prefix  string
	varName string
	suffix  string
	handler http.HandlerFunc
}

// HandleFunc registers a handler for the given pattern. If the pattern conflicts
// with an already-registered ServeMux pattern, it falls back to prefix-based matching.
func (m *lxdMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	if !m.tryRegister(pattern, handler) {
		// Parse the pattern to extract prefix, variable name, and suffix.
		// For example, "images/{fingerprint}/export" becomes:
		// prefix="/1.0/images/", varName="fingerprint", suffix="/export".
		start := strings.Index(pattern, "{")
		end := strings.Index(pattern, "}")
		if start == -1 || end == -1 || end <= start {
			panic(fmt.Sprintf("Conflicting route pattern without parsable path variable: %q", pattern))
		}

		prefix := pattern[:start]
		suffix := pattern[end+1:]

		// Guard against duplicate registrations: a second call with the same
		// conflicting pattern would append an unreachable entry.
		for _, existing := range m.prefixHandlers {
			if existing.prefix == prefix && existing.suffix == suffix {
				panic(fmt.Sprintf("Duplicate conflicting route registration for pattern %q", pattern))
			}
		}

		m.prefixHandlers = append(m.prefixHandlers, lxdPrefixHandler{
			prefix:  prefix,
			varName: strings.TrimSuffix(pattern[start+1:end], "..."),
			suffix:  suffix,
			handler: handler,
		})
	}
}

// tryRegister attempts to register a pattern on the underlying ServeMux.
// Returns false if ServeMux panics due to a routing conflict. Any other
// panic is re-raised so that unexpected issues are not silently swallowed.
func (m *lxdMux) tryRegister(pattern string, handler http.HandlerFunc) (registered bool) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		msg := fmt.Sprint(r)

		// ServeMux panics with a message containing "conflicts with pattern"
		// when two patterns overlap. Verified against Go 1.22-1.26. If Go
		// changes the wording, tryRegister will re-panic (safe failure)
		// instead of silently dropping routes.
		if !strings.Contains(msg, "conflicts with pattern") {
			panic(r)
		}

		// Distinguish a legitimate overlap (two different patterns that
		// conflict) from an accidental exact duplicate. Go's panic message
		// for ServeMux conflicts is of the form:
		//   pattern "A" ... conflicts with pattern "B" ...
		// If our pattern appears twice, A == B: the caller registered the
		// exact same pattern twice, which is a programmer error.
		quoted := fmt.Sprintf("%q", pattern)
		if strings.Count(msg, quoted) >= 2 {
			panic(r)
		}

		registered = false
	}()

	m.ServeMux.HandleFunc(pattern, handler)

	return true
}

func (m *lxdMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// note that we prefer ServeMux when it has a matching pattern. This ensures
	// that directly-registered routes take priority over prefix fallback
	// handlers, which matters when both could match. like
	// /images/aliases/export matches both "images/aliases/{name}" on ServeMux
	// and the prefix handler for "images/{fingerprint}/export". See
	// https://github.com/canonical/lxd/pull/17856
	_, pattern := m.Handler(r)
	if pattern != "" && pattern != "/" {
		m.ServeMux.ServeHTTP(w, r)
		return
	}

	// prefix handlers
	escapedPath := r.URL.EscapedPath()
	pathLen := len(escapedPath)

	for _, ph := range m.prefixHandlers {
		preLen := len(ph.prefix)
		sufLen := len(ph.suffix)

		if pathLen < preLen+sufLen || !strings.HasPrefix(escapedPath, ph.prefix) {
			continue
		}

		remainder := escapedPath[preLen:]
		if !strings.HasSuffix(remainder, ph.suffix) {
			continue
		}

		val := remainder[:len(remainder)-sufLen]

		if val == "" || strings.Contains(val, "/") {
			// encoded slashes (%2f) are fine and stay as-is.
			continue
		}

		decoded, err := url.PathUnescape(val)
		if err != nil {
			decoded = val
		}

		r.SetPathValue(ph.varName, decoded)
		ph.handler(w, r)

		return
	}

	// a legit 404.
	m.ServeMux.ServeHTTP(w, r)
}

type lxdHTTPServer struct {
	r http.Handler
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
