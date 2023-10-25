package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery/agent"

	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// Errors.
var errUnknownUser = api.StatusErrorf(http.StatusForbidden, "Unknown RBAC user")

// rbac represents an RBAC server.
type rbac struct {
	commonAuthorizer
	tls             *tls
	apiURL          string
	agentPrivateKey string
	agentPublicKey  string
	agentAuthURL    string
	agentUsername   string
	projectsGetFunc func(ctx context.Context) (map[int64]string, error)

	lastSyncID string
	client     *httpbakery.Client
	lastChange string

	ctx       context.Context
	ctxCancel context.CancelFunc

	resources     map[string]string // Maps name to identifier
	resourcesLock sync.RWMutex

	// Permission cache of username to map of project name to slice of Permission
	permissions     map[string]map[string][]Permission
	permissionsLock sync.RWMutex
}

func (r *rbac) load(ctx context.Context, certificateCache *certificate.Cache, opts Opts) error {
	err := r.configure(opts)
	if err != nil {
		return err
	}

	// Setup context
	r.ctx, r.ctxCancel = context.WithCancel(context.Background())

	var keyPair bakery.KeyPair
	err = keyPair.Private.UnmarshalText([]byte(r.agentPrivateKey))
	if err != nil {
		return err
	}

	err = keyPair.Public.UnmarshalText([]byte(r.agentPublicKey))
	if err != nil {
		return err
	}

	r.client = httpbakery.NewClient()
	authInfo := agent.AuthInfo{
		Key: &keyPair,
		Agents: []agent.Agent{
			{
				URL:      r.agentAuthURL,
				Username: r.agentUsername,
			},
		},
	}

	err = agent.SetUpAuth(r.client, &authInfo)
	if err != nil {
		return err
	}

	r.client.Client.Jar, err = cookiejar.New(nil)
	if err != nil {
		return err
	}

	// Perform full sync when online
	go func() {
		for {
			err = r.syncProjects(r.ctx)
			if err != nil {
				time.Sleep(time.Minute)
				continue
			}

			break
		}
	}()

	r.tls = &tls{}
	err = r.tls.load(ctx, certificateCache, opts)
	if err != nil {
		return err
	}

	r.startStatusCheck()

	return nil
}

func (r *rbac) configure(opts Opts) error {
	if opts.config == nil {
		return fmt.Errorf("Missing RBAC configuration")
	}

	val, ok := opts.config["rbac.agent.private_key"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.private_key")
	}

	r.agentPrivateKey = val.(string)

	val, ok = opts.config["rbac.agent.public_key"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.public_key")
	}

	r.agentPublicKey = val.(string)

	val, ok = opts.config["rbac.agent.url"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.url")
	}

	r.agentAuthURL = val.(string)

	val, ok = opts.config["rbac.agent.username"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.username")
	}

	r.agentUsername = val.(string)

	val, ok = opts.config["rbac.api.url"]
	if !ok {
		return fmt.Errorf("Missing rbac.api.url")
	}

	r.apiURL = val.(string)

	if opts.projectsGetFunc == nil {
		return fmt.Errorf("Missing projects hook for RBAC driver")
	}

	r.projectsGetFunc = opts.projectsGetFunc

	return nil
}

// CheckPermission syncs the users permissions with the RBAC server, then maps the given Object and Entitlement to an RBAC permission
// and checks this against the users permissions.
func (r *rbac) CheckPermission(ctx context.Context, req *http.Request, object Object, entitlement Entitlement) error {
	details, err := r.requestDetails(req)
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return nil
	}

	// Use the TLS driver if the user authenticated with TLS.
	if details.authenticationProtocol() == api.AuthenticationMethodTLS {
		return r.tls.CheckPermission(ctx, req, object, entitlement)
	}

	r.permissionsLock.Lock()
	defer r.permissionsLock.Unlock()

	username := details.username()
	permissions, ok := r.permissions[username]
	if !ok {
		err := r.syncPermissions(ctx, username)
		if err != nil {
			return fmt.Errorf("Failed to sync user permissions with RBAC server: %w", err)
		}

		permissions, ok = r.permissions[username]
		if !ok {
			return errUnknownUser
		}
	}

	if shared.ValueInSlice(PermissionAdmin, permissions[""]) {
		// Admin
		return nil
	}

	if details.isAllProjectsRequest {
		// Only admins can use the all-projects parameter.
		return api.StatusErrorf(http.StatusForbidden, "User is not an administrator")
	}

	// Check server level object types
	switch object.Type() {
	case ObjectTypeServer:
		if entitlement == EntitlementCanView || entitlement == EntitlementCanViewResources || entitlement == EntitlementCanViewMetrics {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "User is not an administrator")
	case ObjectTypeStoragePool, ObjectTypeCertificate:
		if entitlement == EntitlementCanView {
			return nil
		}

		return api.StatusErrorf(http.StatusForbidden, "User is not an administrator")
	}

	permission, err := r.relationToPermission(object, entitlement)
	if err != nil {
		return err
	}

	projectName := object.Project()
	if !shared.ValueInSlice(permission, permissions[projectName]) {
		return api.StatusErrorf(http.StatusForbidden, "User %q does not have permission %q on project %q", username, permission, projectName)
	}

	return nil
}

// GetPermissionChecker syncs the users permissions with the RBAC server, then in the returned PermissionChecker maps the
// given Object and Entitlement to an RBAC permission and checks this against the users permissions.
func (r *rbac) GetPermissionChecker(ctx context.Context, req *http.Request, entitlement Entitlement, objectType ObjectType) (PermissionChecker, error) {
	allowFunc := func(b bool) func(Object) bool {
		return func(Object) bool {
			return b
		}
	}

	details, err := r.requestDetails(req)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return allowFunc(true), nil
	}

	// Use the TLS driver if the user authenticated with TLS.
	if details.authenticationProtocol() == api.AuthenticationMethodTLS {
		return r.tls.GetPermissionChecker(ctx, req, entitlement, objectType)
	}

	r.permissionsLock.Lock()
	defer r.permissionsLock.Unlock()

	username := details.username()
	permissions, ok := r.permissions[username]
	if !ok {
		err := r.syncPermissions(ctx, username)
		if err != nil {
			return nil, fmt.Errorf("Failed to sync user permissions with RBAC server: %w", err)
		}

		permissions, ok = r.permissions[username]
		if !ok {
			return nil, errUnknownUser
		}
	}

	if shared.ValueInSlice(PermissionAdmin, permissions[""]) {
		// Admin user. Allow all.
		return allowFunc(true), nil
	}

	if details.isAllProjectsRequest {
		// Only admins can use the all-projects parameter.
		return nil, api.StatusErrorf(http.StatusForbidden, "User is not an administrator")
	}

	// Check server level object types
	switch objectType {
	case ObjectTypeServer:
		if entitlement == EntitlementCanView || entitlement == EntitlementCanViewResources || entitlement == EntitlementCanViewMetrics {
			return allowFunc(true), nil
		}

		return nil, api.StatusErrorf(http.StatusForbidden, "User is not an administrator")
	case ObjectTypeStoragePool, ObjectTypeCertificate:
		if entitlement == EntitlementCanView {
			return allowFunc(true), nil
		}

		return nil, api.StatusErrorf(http.StatusForbidden, "User is not an administrator")
	}

	// Error if user does not have access to the project (unless we're getting projects, where we want to filter the results).
	_, ok = permissions[details.projectName]
	if !ok && objectType != ObjectTypeProject {
		return nil, api.StatusErrorf(http.StatusForbidden, "User does not have permissions for project %q", details.projectName)
	}

	return func(object Object) bool {
		// Acquire read lock on the permissions cache.
		r.permissionsLock.RLock()
		defer r.permissionsLock.RUnlock()

		permission, err := r.relationToPermission(object, entitlement)
		if err != nil {
			r.logger.Error("Could not convert object and entitlement to RBAC permission", logger.Ctx{"object": object, "entitlement": entitlement, "error": err})
			return false
		}

		return shared.ValueInSlice(permission, permissions[object.Project()])
	}, nil
}

// AddProject adds a new project resource to RBAC.
func (r *rbac) AddProject(ctx context.Context, projectID int64, projectName string) error {
	resource := rbacResource{
		Name:       projectName,
		Identifier: strconv.FormatInt(projectID, 10),
	}

	// Update RBAC
	err := r.postResources(ctx, []rbacResource{resource}, nil, false)
	if err != nil {
		return err
	}

	// Update project map
	r.resourcesLock.Lock()
	r.resources[projectName] = strconv.FormatInt(projectID, 10)
	r.resourcesLock.Unlock()

	return nil
}

// DeleteProject adds a new project resource to RBAC.
func (r *rbac) DeleteProject(ctx context.Context, projectID int64, _ string) error {
	// Update RBAC
	err := r.postResources(ctx, nil, []string{strconv.FormatInt(projectID, 10)}, false)
	if err != nil {
		return err
	}

	// Update project map
	r.resourcesLock.Lock()
	for k, v := range r.resources {
		if v == strconv.FormatInt(projectID, 10) {
			delete(r.resources, k)
			break
		}
	}
	r.resourcesLock.Unlock()

	return nil
}

// RenameProject renames an existing project resource in RBAC.
func (r *rbac) RenameProject(ctx context.Context, projectID int64, oldName string, newName string) error {
	return r.AddProject(ctx, projectID, newName)
}

// StopService stops the periodic status checker.
func (r *rbac) StopService(ctx context.Context) error {
	r.ctxCancel()
	return nil
}

type rbacResource struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

type rbacResourcePost struct {
	LastSyncID *string        `json:"last-sync-id"`
	Updates    []rbacResource `json:"updates,omitempty"`
	Removals   []string       `json:"removals,omitempty"`
}

type rbacResourcePostResponse struct {
	SyncID string `json:"sync-id"`
}

type rbacStatus struct {
	LastChange string `json:"last-change"`
}

// startStatusCheck runs a status checking loop.
func (r *rbac) startStatusCheck() {
	var status rbacStatus

	// Figure out the new URL.
	u, err := url.Parse(r.apiURL)
	if err != nil {
		logger.Errorf("Failed to parse RBAC url: %v", err)
		return
	}

	u.Path = path.Join(u.Path, "/api/service/v1/changes")

	go func() {
		for {
			if r.ctx.Err() != nil {
				return
			}

			if status.LastChange != "" {
				values := url.Values{}
				values.Set("last-change", status.LastChange)
				u.RawQuery = values.Encode()
			}

			req, err := http.NewRequestWithContext(r.ctx, "GET", u.String(), nil)
			if err != nil {
				if err == context.Canceled {
					return
				}

				logger.Errorf("Failed to prepare RBAC query: %v", err)
				return
			}

			resp, err := r.client.Do(req)
			if err != nil {
				if err == context.Canceled {
					return
				}

				// Handle server/load-balancer timeouts, errors aren't properly wrapped so checking string.
				if strings.HasSuffix(err.Error(), "EOF") {
					continue
				}

				logger.Errorf("Failed to connect to RBAC, re-trying: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			if resp.StatusCode == 504 {
				// 504 indicates the server timed out the background connection, just re-connect.
				_ = resp.Body.Close()
				continue
			}

			if resp.StatusCode != 200 {
				// For other errors we assume a server restart and give it a few seconds.
				_ = resp.Body.Close()
				logger.Debugf("RBAC server disconnected, re-connecting. (code=%v)", resp.StatusCode)
				time.Sleep(5 * time.Second)
				continue
			}

			err = json.NewDecoder(resp.Body).Decode(&status)
			_ = resp.Body.Close()
			if err != nil {
				logger.Errorf("Failed to parse RBAC response, re-trying: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			r.lastChange = status.LastChange
			logger.Debugf("RBAC change detected, flushing cache")
			r.flushCache()
		}
	}()
}

func (r *rbac) flushCache() {
	r.permissionsLock.Lock()
	defer r.permissionsLock.Unlock()

	logger.Info("Flushing RBAC permissions cache")

	for k, v := range r.permissions {
		for k := range v {
			delete(v, k)
		}

		delete(r.permissions, k)
	}

	logger.Info("Flushed RBAC permissions cache")
}

func (r *rbac) syncAdmin(ctx context.Context, username string) bool {
	u, err := url.Parse(r.apiURL)
	if err != nil {
		return false
	}

	values := url.Values{}
	values.Set("u", username)
	u.RawQuery = values.Encode()
	u.Path = path.Join(u.Path, "/api/service/v1/resources/lxd/permissions-for-user")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}

	defer func() { _ = resp.Body.Close() }()

	var permissions map[string][]string

	err = json.NewDecoder(resp.Body).Decode(&permissions)
	if err != nil {
		return false
	}

	return shared.ValueInSlice("admin", permissions[""])
}

func (r *rbac) syncPermissions(ctx context.Context, username string) error {
	u, err := url.Parse(r.apiURL)
	if err != nil {
		return err
	}

	values := url.Values{}
	values.Set("u", username)
	u.RawQuery = values.Encode()
	u.Path = path.Join(u.Path, "/api/service/v1/resources/project/permissions-for-user")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	var permissions map[string][]Permission

	err = json.NewDecoder(resp.Body).Decode(&permissions)
	if err != nil {
		return err
	}

	if r.syncAdmin(ctx, username) {
		permissions[""] = []Permission{PermissionAdmin}
	}

	r.resourcesLock.Lock()
	defer r.resourcesLock.Unlock()

	projectPermissions := make(map[string][]Permission)
	for k, v := range permissions {
		if k == "" {
			projectPermissions[k] = v
			continue
		}

		// Look for project name.
		for projectName, resourceID := range r.resources {
			if k != resourceID {
				continue
			}

			projectPermissions[projectName] = v
			break
		}

		// Ignore unknown projects.
	}

	// No need to acquire the lock since the caller (HasPermission) already has it.
	r.permissions[username] = projectPermissions

	return nil
}

// syncProjects updates the list of projects in RBAC.
func (r *rbac) syncProjects(ctx context.Context) error {
	if r.projectsGetFunc == nil {
		return fmt.Errorf("ProjectsFunc isn't configured yet, cannot sync")
	}

	resources := []rbacResource{}
	resourcesMap := map[string]string{}

	// Get all projects
	projects, err := r.projectsGetFunc(ctx)
	if err != nil {
		return err
	}

	// Convert to RBAC format
	for id, name := range projects {
		resources = append(resources, rbacResource{
			Name:       name,
			Identifier: strconv.FormatInt(id, 10),
		})

		resourcesMap[name] = strconv.FormatInt(id, 10)
	}

	// Update RBAC
	err = r.postResources(ctx, resources, nil, true)
	if err != nil {
		return err
	}

	// Update project map
	r.resourcesLock.Lock()
	r.resources = resourcesMap
	r.resourcesLock.Unlock()

	return nil
}

func (r *rbac) postResources(ctx context.Context, updates []rbacResource, removals []string, force bool) error {
	// Make sure that we have a baseline sync in place
	if !force && r.lastSyncID == "" {
		return r.syncProjects(ctx)
	}

	// Generate the URL
	u, err := url.Parse(r.apiURL)
	if err != nil {
		return err
	}

	u.Path = path.Join(u.Path, "/api/service/v1/resources/project")

	// Prepare the request body
	resourcePost := rbacResourcePost{
		Updates:  updates,
		Removals: removals,
	}

	if force {
		resourcePost.LastSyncID = nil
	} else {
		resourcePost.LastSyncID = &r.lastSyncID
	}

	body, err := json.Marshal(&resourcePost)
	if err != nil {
		return err
	}

	// Perform the request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	// Handle errors
	if resp.StatusCode == 409 {
		// Sync IDs don't match, force sync
		return r.syncProjects(ctx)
	} else if resp.StatusCode != http.StatusOK {
		// Something went wrong
		return errors.New(resp.Status)
	}

	// Extract the new SyncID
	var postRespose rbacResourcePostResponse
	err = json.NewDecoder(resp.Body).Decode(&postRespose)
	if err != nil {
		return err
	}

	r.lastSyncID = postRespose.SyncID

	return nil
}

// relationToPermission is a mapping from fine-grained Object and Entitlement permissions to a less fine-grained RBAC Permission.
// This function will error if there is no mapping. This can be the case when an endpoint does not require any permissions, such
// as `GET /1.0/storage-pools`. These should be handled separately.
func (r *rbac) relationToPermission(object Object, entitlement Entitlement) (Permission, error) {
	switch object.Type() {
	case ObjectTypeServer:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionAdmin, nil
		case EntitlementCanCreateStoragePools:
			return PermissionAdmin, nil
		case EntitlementCanCreateProjects:
			return PermissionAdmin, nil
		case EntitlementCanCreateCertificates:
			return PermissionAdmin, nil
		case EntitlementCanOverrideClusterTargetRestriction:
			return PermissionAdmin, nil
		case EntitlementCanViewPrivilegedEvents:
			return PermissionAdmin, nil
		}

	case ObjectTypeCertificate:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionAdmin, nil
		}

	case ObjectTypeStoragePool:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionAdmin, nil
		}

	case ObjectTypeProject:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageProjects, nil
		case EntitlementCanView:
			return PermissionView, nil
		case EntitlementCanCreateInstances:
			return PermissionManageInstances, nil
		case EntitlementCanCreateImages:
			return PermissionManageImages, nil
		case EntitlementCanCreateImageAliases:
			return PermissionManageImages, nil
		case EntitlementCanCreateNetworks:
			return PermissionManageNetworks, nil
		case EntitlementCanCreateNetworkACLs:
			return PermissionManageNetworks, nil
		case EntitlementCanCreateNetworkZones:
			return PermissionManageNetworks, nil
		case EntitlementCanCreateProfiles:
			return PermissionManageProfiles, nil
		case EntitlementCanCreateStorageVolumes:
			return PermissionManageStorageVolumes, nil
		case EntitlementCanCreateStorageBuckets:
			return PermissionManageStorageVolumes, nil
		case EntitlementCanViewOperations:
			return PermissionView, nil
		case EntitlementCanViewEvents:
			return PermissionView, nil
		}

	case ObjectTypeImage:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageImages, nil
		case EntitlementCanView:
			return PermissionView, nil
		}

	case ObjectTypeImageAlias:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageImages, nil
		case EntitlementCanView:
			return PermissionView, nil
		}

	case ObjectTypeInstance:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageInstances, nil
		case EntitlementCanView:
			return PermissionView, nil
		case EntitlementCanUpdateState:
			return PermissionOperateInstances, nil
		case EntitlementCanManageBackups:
			return PermissionOperateInstances, nil
		case EntitlementCanManageSnapshots:
			return PermissionOperateInstances, nil
		case EntitlementCanConnectSFTP:
			return PermissionOperateInstances, nil
		case EntitlementCanAccessFiles:
			return PermissionOperateInstances, nil
		case EntitlementCanAccessConsole:
			return PermissionOperateInstances, nil
		case EntitlementCanExec:
			return PermissionOperateInstances, nil
		}

	case ObjectTypeNetwork:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageNetworks, nil
		case EntitlementCanView:
			return PermissionView, nil
		}

	case ObjectTypeNetworkACL:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageNetworks, nil
		case EntitlementCanView:
			return PermissionView, nil
		}

	case ObjectTypeNetworkZone:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageNetworks, nil
		case EntitlementCanView:
			return PermissionView, nil
		}

	case ObjectTypeProfile:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageProfiles, nil
		case EntitlementCanView:
			return PermissionView, nil
		}

	case ObjectTypeStorageBucket:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageStorageVolumes, nil
		case EntitlementCanView:
			return PermissionView, nil
		}

	case ObjectTypeStorageVolume:
		switch entitlement {
		case EntitlementCanEdit:
			return PermissionManageStorageVolumes, nil
		case EntitlementCanManageBackups:
			return PermissionManageStorageVolumes, nil
		case EntitlementCanManageSnapshots:
			return PermissionManageStorageVolumes, nil
		case EntitlementCanView:
			return PermissionView, nil
		}
	}

	return "", fmt.Errorf("Could not map object %q and entitlement %q to an RBAC permission", object, entitlement)
}
