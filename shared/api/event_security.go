package api

// EventSecurity represents a security event entry in the events API.
//
// The events API uses Go-style field names. The OWASP-named, fully populated
// audit log entry is produced only at the Loki forwarder.
//
// API extension: event_security.
type EventSecurity struct {
	// Security event identifier (e.g. "authn_login_fail", "authz_fail").
	// Example: authn_login_fail
	Name string `json:"name" yaml:"name"`

	// Severity level (e.g. "info", "warning").
	// Example: warning
	Level string `json:"level" yaml:"level"`

	// Human-readable description of the event.
	// Example: Authentication failure on required endpoint
	Description string `json:"description" yaml:"description"`

	// API path of the inbound request. Empty for daemon-level events.
	// Example: /1.0/instances
	RequestPath string `json:"request_path,omitempty" yaml:"request_path,omitempty"`

	// Project the request targeted. Empty for daemon-level events.
	// Example: default
	Project string `json:"project,omitempty" yaml:"project,omitempty"`

	// Request HTTP method. Empty for daemon-level events.
	// Example: GET
	RequestMethod string `json:"request_method,omitempty" yaml:"request_method,omitempty"`

	// Caller details. Nil for daemon-level events (sys_startup,
	// sys_shutdown, sys_monitor_disabled).
	Requestor *EventSecurityRequestor `json:"requestor,omitempty" yaml:"requestor,omitempty"`
}

// EventSecurityRequestor identifies the caller that triggered a security event.
//
// API extension: event_security.
type EventSecurityRequestor struct {
	// Caller username (e.g. TLS fingerprint, OIDC email, bearer-token subject).
	// Example: jane@example.com
	Username string `json:"username" yaml:"username"`

	// Authentication protocol used by the caller (e.g. "tls", "oidc", "bearer").
	// Example: oidc
	Protocol string `json:"protocol" yaml:"protocol"`

	// Originating address of the caller.
	// Example: 10.0.2.15
	Address string `json:"address" yaml:"address"`

	// HTTP User-Agent header value sent by the caller.
	// Example: curl/8.5.0
	UserAgent string `json:"user_agent" yaml:"user_agent"`
}
