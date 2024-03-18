package lxd

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Server handling functions

// GetServer returns the server status as a Server struct.
func (r *ProtocolLXD) GetServer() (*api.Server, string, error) {
	server := api.Server{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", "", nil, "", &server)
	if err != nil {
		return nil, "", err
	}

	// Fill in certificate fingerprint if not provided
	if server.Environment.CertificateFingerprint == "" && server.Environment.Certificate != "" {
		var err error
		server.Environment.CertificateFingerprint, err = shared.CertFingerprintStr(server.Environment.Certificate)
		if err != nil {
			return nil, "", err
		}
	}

	if !server.Public && len(server.AuthMethods) == 0 {
		// TLS is always available for LXD servers
		server.AuthMethods = []string{api.AuthenticationMethodTLS}
	}

	// Add the value to the cache
	r.server = &server

	return &server, etag, nil
}

// UpdateServer updates the server status to match the provided Server struct.
func (r *ProtocolLXD) UpdateServer(server api.ServerPut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", "", server, ETag)
	if err != nil {
		return err
	}

	return nil
}

// HasExtension returns true if the server supports a given API extension.
// Deprecated: Use CheckExtension instead.
func (r *ProtocolLXD) HasExtension(extension string) bool {
	// If no cached API information, just assume we're good
	// This is needed for those rare cases where we must avoid a GetServer call
	if r.server == nil {
		return true
	}

	return shared.ValueInSlice(extension, r.server.APIExtensions)
}

// CheckExtension checks if the server has the specified extension.
func (r *ProtocolLXD) CheckExtension(extensionName string) error {
	if !r.HasExtension(extensionName) {
		return fmt.Errorf("The server is missing the required %q API extension", extensionName)
	}

	return nil
}

// IsClustered returns true if the server is part of a LXD cluster.
func (r *ProtocolLXD) IsClustered() bool {
	return r.server.Environment.ServerClustered
}

// GetServerResources returns the resources available to a given LXD server.
func (r *ProtocolLXD) GetServerResources() (*api.Resources, error) {
	err := r.CheckExtension("resources")
	if err != nil {
		return nil, err
	}

	resources := api.Resources{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", "/resources", nil, "", &resources)
	if err != nil {
		return nil, err
	}

	return &resources, nil
}

// UseProject returns a client that will use a specific project.
func (r *ProtocolLXD) UseProject(name string) InstanceServer {
	return &ProtocolLXD{
		ctx:                    r.ctx,
		ctxConnected:           r.ctxConnected,
		ctxConnectedCancel:     r.ctxConnectedCancel,
		server:                 r.server,
		http:                   r.http,
		httpCertificate:        r.httpCertificate,
		httpBaseURL:            r.httpBaseURL,
		httpProtocol:           r.httpProtocol,
		httpUserAgent:          r.httpUserAgent,
		supportsAuthentication: r.supportsAuthentication,
		requireAuthenticated:   r.requireAuthenticated,
		clusterTarget:          r.clusterTarget,
		project:                name,
		eventConns:             make(map[string]*websocket.Conn),  // New project specific listener conns.
		eventListeners:         make(map[string][]*EventListener), // New project specific listeners.
		oidcClient:             r.oidcClient,
	}
}

// UseTarget returns a client that will target a specific cluster member.
// Use this member-specific operations such as specific container
// placement, preparing a new storage pool or network, ...
func (r *ProtocolLXD) UseTarget(name string) InstanceServer {
	return &ProtocolLXD{
		ctx:                    r.ctx,
		ctxConnected:           r.ctxConnected,
		ctxConnectedCancel:     r.ctxConnectedCancel,
		server:                 r.server,
		http:                   r.http,
		httpCertificate:        r.httpCertificate,
		httpBaseURL:            r.httpBaseURL,
		httpProtocol:           r.httpProtocol,
		httpUserAgent:          r.httpUserAgent,
		supportsAuthentication: r.supportsAuthentication,
		requireAuthenticated:   r.requireAuthenticated,
		project:                r.project,
		eventConns:             make(map[string]*websocket.Conn),  // New target specific listener conns.
		eventListeners:         make(map[string][]*EventListener), // New target specific listeners.
		oidcClient:             r.oidcClient,
		clusterTarget:          name,
	}
}

// IsAgent returns true if the server is a LXD agent.
func (r *ProtocolLXD) IsAgent() bool {
	return r.server != nil && r.server.Environment.Server == "lxd-agent"
}

// GetMetrics returns the text OpenMetrics data.
func (r *ProtocolLXD) GetMetrics() (string, error) {
	// Check that the server supports it.
	err := r.CheckExtension("metrics")
	if err != nil {
		return "", err
	}

	// Prepare the request.
	requestURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0/metrics", r.httpBaseURL.String()))
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", err
	}

	// Send the request.
	resp, err := r.DoHTTP(req)
	if err != nil {
		return "", err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Bad HTTP status: %d", resp.StatusCode)
	}

	// Get the content.
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}
