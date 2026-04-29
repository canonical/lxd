// Package security contains the audit-event vocabulary used to satisfy the
// logging requirements: a fixed set of action identifiers and severity levels,
// plus the helpers that build api.EventSecurity values for request-scoped and
// daemon-level audit events.
package security

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
)

// SecurityLevel represents the severity level of a security event.
type SecurityLevel string

// Security event severity levels.
const (
	// LevelInfo is used for normal administrative events.
	LevelInfo SecurityLevel = "info"

	// LevelWarning is used for suspicious or unexpected activity.
	LevelWarning SecurityLevel = "warning"
)

// SecurityAction represents a security event action.
type SecurityAction string

// All supported security event actions.
const (
	AuthnLoginFail         SecurityAction = "authn_login_fail"
	AuthnCertificateChange SecurityAction = "authn_certificate_change"
	AuthnTokenCreated      SecurityAction = "authn_token_created"
	AuthnTokenRevoked      SecurityAction = "authn_token_revoked"
	AuthnTokenReuse        SecurityAction = "authn_token_reuse"
	AuthzFail              SecurityAction = "authz_fail"
	AuthzAdmin             SecurityAction = "authz_admin"
	SysStartup             SecurityAction = "sys_startup"
	SysShutdown            SecurityAction = "sys_shutdown"
	SysMonitorDisabled     SecurityAction = "sys_monitor_disabled"
	UserCreated            SecurityAction = "user_created"
	UserUpdated            SecurityAction = "user_updated"
	UserDeleted            SecurityAction = "user_deleted"
)

// WithSuffix appends colon-separated context parts to the action identifier,
// following the spec convention (e.g. authz_fail:can_edit:/1.0/projects/foo).
func (a SecurityAction) WithSuffix(parts ...string) SecurityAction {
	if len(parts) == 0 {
		return a
	}

	return SecurityAction(string(a) + ":" + strings.Join(parts, ":"))
}

// httpRequestAuditInfo holds the per-request fields that do not change across
// audit events raised within the same HTTP request. Stored on the request
// context so handler code can build events without re-parsing *http.Request.
type httpRequestAuditInfo struct {
	userAgent     string
	requestPath   string
	requestMethod string
	project       string
	sourceIP      string
}

// InitRequestAuditInfo extracts the base fields from r and stashes them on the
// request context under request.CtxSecurityEventBase. Call this once at the
// HTTP entry point; downstream handlers can then build audit events from
// r.Context() without ever touching *http.Request again.
func InitRequestAuditInfo(r *http.Request) {
	// project is read raw rather than via request.ProjectParam so that
	// requests not scoped to a project (server-level URLs, daemon-level
	// events) leave the field empty instead of falsely claiming "default".
	info := &httpRequestAuditInfo{
		userAgent:     r.Header.Get("User-Agent"),
		requestPath:   r.URL.Path,
		requestMethod: r.Method,
		project:       request.QueryParam(r, "project"),
	}

	// Stash the client address so audit events raised before authentication
	// completes (e.g. bearer-auth failures) still have a non-empty source
	// address. Forwarded cluster-internal requests later override this with
	// the upstream caller's address via the requestor.
	if r.RemoteAddr != "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			info.sourceIP = host
		} else {
			info.sourceIP = r.RemoteAddr
		}
	}

	request.SetContextValue(r, request.CtxSecurityEventBase, info)
}

// UserEventOption mutates an in-progress audit event built by UserEvent.
type UserEventOption func(*api.EventSecurity)

// WithRequestorOverride merges the supplied requestor onto the in-progress
// event. Only fields set on override take effect; unset fields keep the
// values populated from the request audit info. Use this for failed-auth
// paths where the caller identity is known (e.g. the JWT subject of a
// rejected bearer token) but no Requestor has been built on the context yet.
func WithRequestorOverride(override *api.EventSecurityRequestor) UserEventOption {
	return func(ev *api.EventSecurity) {
		if override == nil {
			return
		}

		if ev.Requestor == nil {
			ev.Requestor = &api.EventSecurityRequestor{}
		}

		if override.Username != "" {
			ev.Requestor.Username = override.Username
		}

		if override.Protocol != "" {
			ev.Requestor.Protocol = override.Protocol
		}

		if override.Address != "" {
			ev.Requestor.Address = override.Address
		}

		if override.UserAgent != "" {
			ev.Requestor.UserAgent = override.UserAgent
		}
	}
}

// UserEvent builds an api.EventSecurity for a request-scoped audit event,
// reading the base fields previously stashed on ctx by InitRequestAuditInfo
// and the caller identity from the requestor associated with ctx.
func (a SecurityAction) UserEvent(ctx context.Context, level SecurityLevel, description string, opts ...UserEventOption) *api.EventSecurity {
	ev := &api.EventSecurity{
		Name:        string(a),
		Level:       string(level),
		Description: description,
	}

	requestor, err := request.GetRequestorAuditor(ctx)
	hasRequestor := err == nil && requestor != nil

	info, err := request.GetContextValue[*httpRequestAuditInfo](ctx, request.CtxSecurityEventBase)
	if err == nil && info != nil {
		ev.RequestPath = info.requestPath
		ev.RequestMethod = info.requestMethod
		ev.Project = info.project

		// Populate the requestor from the per-request audit info even when
		// authentication has not yet completed, so that pre-auth events
		// (authn_token_reuse, authn_login_fail) still record the caller's
		// origin address and user agent.
		ev.Requestor = &api.EventSecurityRequestor{
			UserAgent: info.userAgent,
			Address:   info.sourceIP,
		}
	}

	if hasRequestor {
		if ev.Requestor == nil {
			ev.Requestor = &api.EventSecurityRequestor{}
		}

		// Prefer the requestor's origin address (which honours forwarded
		// cluster-internal requests) over the raw RemoteAddr fallback.
		originAddress := requestor.OriginAddress()
		if originAddress != "" {
			ev.Requestor.Address = originAddress
		}

		ev.Requestor.Protocol = requestor.CallerProtocol()
		ev.Requestor.Username = requestor.CallerUsername()
	}

	for _, opt := range opts {
		opt(ev)
	}

	return ev
}

// ServerEvent builds an api.EventSecurity for a daemon-level event that has no
// associated HTTP request (sys_startup, sys_shutdown, sys_monitor_disabled).
func (a SecurityAction) ServerEvent(level SecurityLevel, description string) *api.EventSecurity {
	return &api.EventSecurity{
		Name:        string(a),
		Level:       string(level),
		Description: description,
	}
}
