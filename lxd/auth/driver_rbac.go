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

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

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

// Errors.
var errUnknownUser = fmt.Errorf("Unknown RBAC user")

// rbac represents an RBAC server.
type rbac struct {
	commonAuthorizer
	tls

	apiURL          string
	agentPrivateKey string
	agentPublicKey  string
	agentAuthURL    string
	agentUsername   string

	lastSyncID string
	client     *httpbakery.Client
	lastChange string

	ctx       context.Context
	ctxCancel context.CancelFunc

	resources     map[string]string // Maps name to identifier
	resourcesLock sync.Mutex

	permissions map[string]map[string][]string

	permissionsLock *sync.Mutex
}

func (r *rbac) load() error {
	err := r.validateConfig()
	if err != nil {
		return err
	}

	r.permissionsLock = &sync.Mutex{}

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
			err = r.syncProjects()
			if err != nil {
				time.Sleep(time.Minute)
				continue
			}

			break
		}
	}()

	r.startStatusCheck()

	return nil
}

func (r *rbac) validateConfig() error {
	val, ok := r.config["rbac.agent.private_key"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.private_key")
	}

	r.agentPrivateKey = val.(string)

	val, ok = r.config["rbac.agent.public_key"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.public_key")
	}

	r.agentPublicKey = val.(string)

	val, ok = r.config["rbac.agent.url"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.url")
	}

	r.agentAuthURL = val.(string)

	val, ok = r.config["rbac.agent.username"]
	if !ok {
		return fmt.Errorf("Missing rbac.agent.username")
	}

	r.agentUsername = val.(string)

	val, ok = r.config["rbac.api.url"]
	if !ok {
		return fmt.Errorf("Missing rbac.api.url")
	}

	r.apiURL = val.(string)

	return nil
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

// StopStatusCheck stops the periodic status checker.
func (r *rbac) StopStatusCheck() {
	r.ctxCancel()
}

// syncProjects updates the list of projects in RBAC.
func (r *rbac) syncProjects() error {
	if r.projectsGetFunc == nil {
		return fmt.Errorf("ProjectsFunc isn't configured yet, cannot sync")
	}

	resources := []rbacResource{}
	resourcesMap := map[string]string{}

	// Get all projects
	projects, err := r.projectsGetFunc()
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
	err = r.postResources(resources, nil, true)
	if err != nil {
		return err
	}

	// Update project map
	r.resourcesLock.Lock()
	r.resources = resourcesMap
	r.resourcesLock.Unlock()

	return nil
}

// AddProject adds a new project resource to RBAC.
func (r *rbac) AddProject(projectID int64, name string) error {
	resource := rbacResource{
		Name:       name,
		Identifier: strconv.FormatInt(projectID, 10),
	}

	// Update RBAC
	err := r.postResources([]rbacResource{resource}, nil, false)
	if err != nil {
		return err
	}

	// Update project map
	r.resourcesLock.Lock()
	r.resources[name] = strconv.FormatInt(projectID, 10)
	r.resourcesLock.Unlock()

	return nil
}

// DeleteProject adds a new project resource to RBAC.
func (r *rbac) DeleteProject(projectID int64) error {
	// Update RBAC
	err := r.postResources(nil, []string{strconv.FormatInt(projectID, 10)}, false)
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
func (r *rbac) RenameProject(projectID int64, name string) error {
	return r.AddProject(projectID, name)
}

// UserAccess returns a UserAccess struct for the user.
func (r *rbac) UserAccess(username string) (*UserAccess, error) {
	r.permissionsLock.Lock()
	defer r.permissionsLock.Unlock()

	// Check whether the permissions are cached.
	_, cached := r.permissions[username]

	if !cached {
		_ = r.syncPermissions(username)
	}

	// Checked if the user exists.
	permissions, ok := r.permissions[username]
	if !ok {
		return nil, errUnknownUser
	}

	// Prepare the response.
	access := UserAccess{
		Admin:    shared.ValueInSlice("admin", permissions[""]),
		Projects: map[string][]string{},
	}

	for k, v := range permissions {
		// Skip the global permissions.
		if k == "" {
			continue
		}

		// Look for project name.
		for projectName, resourceID := range r.resources {
			if k != resourceID {
				continue
			}

			access.Projects[projectName] = v
			break
		}

		// Ignore unknown projects.
	}

	return &access, nil
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

func (r *rbac) syncAdmin(username string) bool {
	u, err := url.Parse(r.apiURL)
	if err != nil {
		return false
	}

	values := url.Values{}
	values.Set("u", username)
	u.RawQuery = values.Encode()
	u.Path = path.Join(u.Path, "/api/service/v1/resources/lxd/permissions-for-user")

	req, err := http.NewRequest("GET", u.String(), nil)
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

func (r *rbac) syncPermissions(username string) error {
	u, err := url.Parse(r.apiURL)
	if err != nil {
		return err
	}

	values := url.Values{}
	values.Set("u", username)
	u.RawQuery = values.Encode()
	u.Path = path.Join(u.Path, "/api/service/v1/resources/project/permissions-for-user")

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	var permissions map[string][]string

	err = json.NewDecoder(resp.Body).Decode(&permissions)
	if err != nil {
		return err
	}

	if r.syncAdmin(username) {
		permissions[""] = []string{"admin"}
	}

	// No need to acquire the lock since the caller (HasPermission) already has it.
	r.permissions[username] = permissions

	return nil
}

func (r *rbac) postResources(updates []rbacResource, removals []string, force bool) error {
	// Make sure that we have a baseline sync in place
	if !force && r.lastSyncID == "" {
		return r.syncProjects()
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
	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
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
		return r.syncProjects()
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
