package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/canonical/lxd/lxd/auth/bearer"
	"github.com/canonical/lxd/lxd/auth/oidc"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

var apiRoot = []APIEndpoint{
	acmeChallengeCmd,
	bearerLogoutCmd,
	documentationCmd,
	documentationRedirectCmd,
	oidcCallbackCmd,
	oidcLoginCmd,
	oidcLogoutCmd,
	rootCmd,
	uiCmd,
	uiRedirectCmd,
}

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
var rootCmd = APIEndpoint{
	MetricsType: entity.TypeServer,

	Get: APIEndpointAction{Handler: rootGet, AllowUntrusted: true},
}

// acmeChallengeCmd is defined here so it can be included in apiRoot.
// The handler itself lives in acme.go.
var acmeChallengeCmd = APIEndpoint{
	Path: ".well-known/acme-challenge/{token}",

	Get: APIEndpointAction{Handler: acmeProvideChallenge, AllowUntrusted: true},
}

var oidcLoginCmd = APIEndpoint{
	Name: "oidcLogin",
	Path: "oidc/login",

	Get: APIEndpointAction{Handler: oidcLoginGet, AllowUntrusted: true},
}

var oidcCallbackCmd = APIEndpoint{
	Name: "oidcCallback",
	Path: "oidc/callback",

	Get: APIEndpointAction{Handler: oidcCallbackGet, AllowUntrusted: true},
}

var oidcLogoutCmd = APIEndpoint{
	Name: "oidcLogout",
	Path: "oidc/logout",

	Get: APIEndpointAction{Handler: oidcLogoutGet, AllowUntrusted: true},
}

var bearerLogoutCmd = APIEndpoint{
	Name: "bearerLogout",
	Path: "bearer/logout",

	Get: APIEndpointAction{Handler: bearerLogoutGet, AllowUntrusted: true},
}

// uiCmd serves the LXD web UI. The path pattern captures any subpath under /ui/.
var uiCmd = APIEndpoint{
	Path: "ui/{filepath:.*}",

	Get: APIEndpointAction{Handler: uiGet, AllowUntrusted: true},
}

// uiRedirectCmd redirects bare /ui requests to /ui/.
var uiRedirectCmd = APIEndpoint{
	Path: "ui",

	Get: APIEndpointAction{Handler: uiRedirectGet, AllowUntrusted: true},
}

// documentationCmd serves the LXD documentation. The path pattern captures any subpath under /documentation/.
var documentationCmd = APIEndpoint{
	Path: "documentation/{filepath:.*}",

	Get: APIEndpointAction{Handler: documentationGet, AllowUntrusted: true},
}

// documentationRedirectCmd redirects bare /documentation requests to /documentation/.
var documentationRedirectCmd = APIEndpoint{
	Path: "documentation",

	Get: APIEndpointAction{Handler: documentationRedirectGet, AllowUntrusted: true},
}

// uiDisabledMessage is the HTML body returned when the LXD web UI is not configured.
const uiDisabledMessage = `<html><title>The UI is not enabled</title><body><p>The UI is not enabled. For instructions to enable it check: <a href="https://documentation.ubuntu.com/lxd/latest/howto/access_ui/">How to access the LXD web UI</a></p></body></html>`

func rootGet(d *Daemon, r *http.Request) response.Response {
	if isBrowserClient(r) {
		return response.ManualResponse(func(w http.ResponseWriter) error {
			err := handleUIAccessLink(w, r, d.globalConfig.ClusterUUID(), d.identityCache)
			if err != nil {
				http.Redirect(w, r, "/ui/?initial-access-link-invalid", http.StatusFound)
				return nil
			}

			http.Redirect(w, r, "/ui/", http.StatusFound)
			return nil
		})
	}

	return response.SyncResponse(true, []string{"/1.0"})
}

// oidcHandler handles an OIDC-specific request, returning 404 when OIDC is not configured.
// It snapshots d.oidcVerifier once to avoid a TOCTOU race between the nil check and the method call.
//
// fn is typically passed as a method expression such as (*oidc.Verifier).Login.
// A method expression promotes the receiver to the first explicit argument, allowing
// oidcHandler to call fn(verifier, w, r) uniformly for any [oidc.Verifier] method.
func oidcHandler(d *Daemon, r *http.Request, fn func(*oidc.Verifier, http.ResponseWriter, *http.Request)) response.Response {
	verifier := d.oidcVerifier
	if verifier == nil {
		return response.NotFound(nil)
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		fn(verifier, w, r)
		return nil
	})
}

// oidcLoginGet initiates the OIDC browser login (code flow).
func oidcLoginGet(d *Daemon, r *http.Request) response.Response {
	return oidcHandler(d, r, (*oidc.Verifier).Login)
}

// oidcCallbackGet handles the OIDC code exchange callback.
func oidcCallbackGet(d *Daemon, r *http.Request) response.Response {
	return oidcHandler(d, r, (*oidc.Verifier).Callback)
}

// oidcLogoutGet handles the OIDC logout flow.
func oidcLogoutGet(d *Daemon, r *http.Request) response.Response {
	return oidcHandler(d, r, (*oidc.Verifier).Logout)
}

func bearerLogoutGet(d *Daemon, r *http.Request) response.Response {
	return response.ManualResponse(func(w http.ResponseWriter) error {
		http.SetCookie(w, &http.Cookie{
			Name:     bearer.CookieNameSession,
			Value:    "",
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,

			// Expire the cookie to instruct the browser to delete it.
			MaxAge:  -1,
			Expires: time.Unix(0, 0),
		})

		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/ui/login", http.StatusFound)
		return nil
	})
}

func uiGet(d *Daemon, r *http.Request) response.Response {
	uiPath := os.Getenv("LXD_UI")
	if uiPath == "" || !shared.PathExists(uiPath) {
		return response.ManualResponse(func(w http.ResponseWriter) error {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, err := fmt.Fprint(w, uiDisabledMessage)
			if err != nil {
				logger.Warn("Failed sending error message to client", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr, "err": err})
			}

			return nil
		})
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		// Disables the FLoC (Federated Learning of Cohorts) feature on the browser,
		// preventing the current page from being included in the user's FLoC calculation.
		// FLoC is a proposed replacement for third-party cookies to enable interest-based advertising.
		w.Header().Set("Permissions-Policy", "interest-cohort=()")
		// Prevents the browser from trying to guess the MIME type, which can have security implications.
		// This tells the browser to strictly follow the MIME type provided in the Content-Type header.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Restricts the page from being displayed in a frame, iframe, or object to avoid click jacking attacks,
		// but allows it if the site is navigating to the same origin.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		// Sets the Content Security Policy (CSP) for the page, which helps mitigate XSS attacks and data injection attacks.
		// The policy allows loading resources (scripts, styles, images, etc.) only from the same origin ('self'), data URLs, and a restrictive list of domains.
		w.Header().Set("Content-Security-Policy", "default-src 'self' data: https://assets.ubuntu.com https://cloud-images.ubuntu.com https://images.lxd.canonical.com; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'")
		// Prevents the browser from sending referrer information when navigating away from the page.
		w.Header().Set("Referrer-Policy", "no-referrer")

		http.StripPrefix("/ui/", http.FileServer(uiHTTPDir{http.Dir(uiPath)})).ServeHTTP(w, r)
		return nil
	})
}

func uiRedirectGet(d *Daemon, r *http.Request) response.Response {
	uiPath := os.Getenv("LXD_UI")
	if uiPath == "" || !shared.PathExists(uiPath) {
		return response.ManualResponse(func(w http.ResponseWriter) error {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, err := fmt.Fprint(w, uiDisabledMessage)
			if err != nil {
				logger.Warn("Failed sending error message to client", logger.Ctx{"url": r.URL, "method": r.Method, "remote": r.RemoteAddr, "err": err})
			}

			return nil
		})
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
		return nil
	})
}

func documentationGet(d *Daemon, r *http.Request) response.Response {
	documentationPath := os.Getenv("LXD_DOCUMENTATION")
	if documentationPath == "" || !shared.PathExists(documentationPath) {
		return response.NotFound(nil)
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		w.Header().Set("Permissions-Policy", "interest-cohort=()")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "no-referrer")

		http.StripPrefix("/documentation/", http.FileServer(documentationHTTPDir{http.Dir(documentationPath)})).ServeHTTP(w, r)
		return nil
	})
}

func documentationRedirectGet(d *Daemon, r *http.Request) response.Response {
	documentationPath := os.Getenv("LXD_DOCUMENTATION")
	if documentationPath == "" || !shared.PathExists(documentationPath) {
		return response.NotFound(nil)
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		http.Redirect(w, r, "/documentation/", http.StatusMovedPermanently)
		return nil
	})
}

// uiHTTPDir is a custom HTTP filesystem for the LXD web UI.
// It falls back to serving index.html for any missing file, supporting
// client-side routing in single-page applications.
type uiHTTPDir struct {
	http.FileSystem
}

// Open opens a file from the UI filesystem, falling back to index.html when a file is not found.
func (fs uiHTTPDir) Open(name string) (http.File, error) {
	fsFile, err := fs.FileSystem.Open(name)
	if err != nil && os.IsNotExist(err) {
		return fs.FileSystem.Open("index.html")
	}

	return fsFile, err
}

// documentationHTTPDir is a custom HTTP filesystem for the LXD documentation.
// It falls back to serving index.html for any missing file.
type documentationHTTPDir struct {
	http.FileSystem
}

// Open opens a file from the documentation filesystem, falling back to index.html when a file is not found.
func (fs documentationHTTPDir) Open(name string) (http.File, error) {
	fsFile, err := fs.FileSystem.Open(name)
	if err != nil && os.IsNotExist(err) {
		return fs.FileSystem.Open("index.html")
	}

	return fsFile, err
}
