//go:build linux && cgo && !agent

package auth

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

type commonAuthorizer struct {
	driverName string
	logger     logger.Logger
}

func (c *commonAuthorizer) init(driverName string, l logger.Logger) error {
	if l == nil {
		return fmt.Errorf("Cannot initialise authorizer: nil logger provided")
	}

	l = l.AddContext(logger.Ctx{"driver": driverName})

	c.driverName = driverName
	c.logger = l
	return nil
}

type requestDetails struct {
	trusted              bool
	userName             string
	protocol             string
	forwardedUsername    string
	forwardedProtocol    string
	isAllProjectsRequest bool
	projectName          string
	isPKI                bool
	idpGroups            []string
	forwardedIDPGroups   []string
}

func (r *requestDetails) isInternalOrUnix() bool {
	if r.protocol == "unix" {
		return true
	}

	if r.protocol == "cluster" && (r.forwardedProtocol == "unix" || r.forwardedProtocol == "cluster" || r.forwardedProtocol == "") {
		return true
	}

	return false
}

func (r *requestDetails) username() string {
	if r.protocol == "cluster" && r.forwardedUsername != "" {
		return r.forwardedUsername
	}

	return r.userName
}

func (r *requestDetails) authenticationProtocol() string {
	if r.protocol == "cluster" {
		return r.forwardedProtocol
	}

	return r.protocol
}

func (r *requestDetails) identityProviderGroups() []string {
	if r.protocol == "cluster" {
		return r.forwardedIDPGroups
	}

	return r.idpGroups
}

func (c *commonAuthorizer) requestDetails(r *http.Request) (*requestDetails, error) {
	if r == nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Cannot inspect nil request")
	} else if r.URL == nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Request URL is not set")
	}

	var err error
	d := &requestDetails{}

	d.trusted, err = request.GetCtxValue[bool](r.Context(), request.CtxTrusted)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Failed getting authentication status: %w", err)
	}

	// If request is not trusted, no other values should be extracted.
	if !d.trusted {
		return d, nil
	}

	// Request protocol cannot be empty.
	d.protocol, err = request.GetCtxValue[string](r.Context(), request.CtxProtocol)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Failed getting protocol: %w", err)
	}

	// Forwarded protocol can be empty.
	d.forwardedProtocol, _ = request.GetCtxValue[string](r.Context(), request.CtxForwardedProtocol)

	// If we're in a CA environment, it's possible for a certificate to be trusted despite not being present in the trust store.
	// We rely on the validation of the certificate (and its potential revocation) having been done in CheckTrustState.
	d.isPKI = d.authenticationProtocol() == api.AuthenticationMethodTLS && shared.PathExists(shared.VarPath("server.ca"))

	// Username cannot be empty.
	d.userName, err = request.GetCtxValue[string](r.Context(), request.CtxUsername)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Failed getting username: %w", err)
	}

	// Forwarded username can be empty.
	d.forwardedUsername, _ = request.GetCtxValue[string](r.Context(), request.CtxForwardedUsername)

	// Check for identity provider groups.
	d.idpGroups, _ = request.GetCtxValue[[]string](r.Context(), request.CtxIdentityProviderGroups)
	d.forwardedIDPGroups, _ = request.GetCtxValue[[]string](r.Context(), request.CtxForwardedIdentityProviderGroups)

	// Check if the request is for all projects.
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusBadRequest, "Failed to parse request query parameters: %w", err)
	}

	// Get project details.
	d.isAllProjectsRequest = shared.IsTrue(values.Get("all-projects"))
	d.projectName = request.ProjectParam(r)

	return d, nil
}

// Driver returns the driver name.
func (c *commonAuthorizer) Driver() string {
	return c.driverName
}
