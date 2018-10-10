package lxd

import (
	"fmt"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Server handling functions

// GetServer returns the server status as a Server struct
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
		server.AuthMethods = []string{"tls"}
	}

	// Add the value to the cache
	r.server = &server

	return &server, etag, nil
}

// UpdateServer updates the server status to match the provided Server struct
func (r *ProtocolLXD) UpdateServer(server api.ServerPut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", "", server, ETag)
	if err != nil {
		return err
	}

	return nil
}

// HasExtension returns true if the server supports a given API extension
func (r *ProtocolLXD) HasExtension(extension string) bool {
	// If no cached API information, just assume we're good
	// This is needed for those rare cases where we must avoid a GetServer call
	if r.server == nil {
		return true
	}

	for _, entry := range r.server.APIExtensions {
		if entry == extension {
			return true
		}
	}

	return false
}

// IsClustered returns true if the server is part of a LXD cluster.
func (r *ProtocolLXD) IsClustered() bool {
	return r.server.Environment.ServerClustered
}

// GetServerResources returns the resources available to a given LXD server
func (r *ProtocolLXD) GetServerResources() (*api.Resources, error) {
	if !r.HasExtension("resources") {
		return nil, fmt.Errorf("The server is missing the required \"resources\" API extension")
	}

	resources := api.Resources{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/resources", nil, "", &resources)
	if err != nil {
		return nil, err
	}

	return &resources, nil
}

// UseProject returns a client that will use a specific project.
func (r *ProtocolLXD) UseProject(name string) ContainerServer {
	return &ProtocolLXD{
		server:               r.server,
		http:                 r.http,
		httpCertificate:      r.httpCertificate,
		httpHost:             r.httpHost,
		httpProtocol:         r.httpProtocol,
		httpUserAgent:        r.httpUserAgent,
		bakeryClient:         r.bakeryClient,
		bakeryInteractor:     r.bakeryInteractor,
		requireAuthenticated: r.requireAuthenticated,
		clusterTarget:        r.clusterTarget,
		project:              name,
	}
}

// UseTarget returns a client that will target a specific cluster member.
// Use this member-specific operations such as specific container
// placement, preparing a new storage pool or network, ...
func (r *ProtocolLXD) UseTarget(name string) ContainerServer {
	return &ProtocolLXD{
		server:               r.server,
		http:                 r.http,
		httpCertificate:      r.httpCertificate,
		httpHost:             r.httpHost,
		httpProtocol:         r.httpProtocol,
		httpUserAgent:        r.httpUserAgent,
		bakeryClient:         r.bakeryClient,
		bakeryInteractor:     r.bakeryInteractor,
		requireAuthenticated: r.requireAuthenticated,
		project:              r.project,
		clusterTarget:        name,
	}
}
