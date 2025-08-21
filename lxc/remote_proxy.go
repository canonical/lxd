package main

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

type remoteProxyTransport struct {
	s lxd.InstanceServer

	baseURL *url.URL
}

// RoundTrip handles an HTTP request.
func (t remoteProxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Fix the request.
	r.URL.Scheme = t.baseURL.Scheme
	r.URL.Host = t.baseURL.Host
	r.RequestURI = ""

	return t.s.DoHTTP(r)
}

type remoteProxyHandler struct {
	s         lxd.InstanceServer
	transport http.RoundTripper

	mu           *sync.RWMutex
	connections  *uint64
	transactions *uint64

	api10     *api.Server
	api10Etag string

	token string
}

func (h remoteProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Increase counters.
	defer func() {
		h.mu.Lock()
		*h.connections -= 1
		h.mu.Unlock()
	}()

	h.mu.Lock()
	*h.transactions += 1
	*h.connections += 1
	h.mu.Unlock()

	// Basic auth.
	if h.token != "" {
		// Parse query URL.
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return
		}

		token := values.Get("auth_token")
		if token != "" {
			tokenCookie := http.Cookie{
				Name:     "auth_token",
				Value:    token,
				Path:     "/",
				Secure:   false,
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			}

			http.SetCookie(w, &tokenCookie)
		} else {
			cookie, err := r.Cookie("auth_token")
			if err != nil || cookie.Value != h.token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
	}

	// Handle /1.0 internally (saves a round-trip).
	if r.RequestURI == "/1.0" || strings.HasPrefix(r.RequestURI, "/1.0?project=") {
		// Parse query URL.
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return
		}

		// Update project name to match.
		projectName := values.Get("project")
		if projectName == "" {
			projectName = api.ProjectDefaultName
		}

		api10 := api.Server(*h.api10)
		api10.Environment.Project = projectName

		// Set the request headers.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", h.api10Etag)
		w.WriteHeader(http.StatusOK)

		// Generate a body from the cached data.
		serverBody, err := json.Marshal(api10)
		if err != nil {
			return
		}

		apiResponse := api.Response{
			Type:       "sync",
			Status:     "success",
			StatusCode: 200,
			Metadata:   serverBody,
		}

		body, err := json.Marshal(apiResponse)
		if err != nil {
			return
		}

		_, _ = w.Write(body)

		return
	}

	// Forward everything else.
	proxy := httputil.ReverseProxy{
		Transport: h.transport,
		Director:  func(*http.Request) {},
	}

	proxy.ServeHTTP(w, r)
}
