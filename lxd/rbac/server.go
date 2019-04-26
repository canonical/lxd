package rbac

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strconv"
	"sync"
	"time"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/agent"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
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
	LastChange time.Time `json:"last-change"`
}

// Server represents an RBAC server.
type Server struct {
	apiURL string
	apiKey string

	lastSyncID string
	client     *httpbakery.Client
	lastChange time.Time
	statusDone chan int

	resources     map[string]string // Maps name to identifier
	resourcesLock sync.Mutex

	permissions map[string]map[string][]string

	permissionsLock *sync.Mutex

	ProjectsFunc func() (map[int64]string, error)
}

// NewServer returns a new RBAC server instance.
func NewServer(apiURL string, apiKey string, agentAuthURL string, agentUsername string, agentPrivateKey string, agentPublicKey string) (*Server, error) {
	r := Server{
		apiURL:          apiURL,
		apiKey:          apiKey,
		lastSyncID:      "",
		lastChange:      time.Time{},
		resources:       make(map[string]string),
		permissions:     make(map[string]map[string][]string),
		permissionsLock: &sync.Mutex{},
	}

	//
	var keyPair bakery.KeyPair
	keyPair.Private.UnmarshalText([]byte(agentPrivateKey))
	keyPair.Public.UnmarshalText([]byte(agentPublicKey))

	r.client = httpbakery.NewClient()
	authInfo := agent.AuthInfo{
		Key: &keyPair,
		Agents: []agent.Agent{
			{
				URL:      agentAuthURL,
				Username: agentUsername,
			},
		},
	}

	err := agent.SetUpAuth(r.client, &authInfo)
	if err != nil {
		return nil, err
	}

	r.client.Client.Jar, err = cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	return &r, nil
}

// StartStatusCheck starts a periodic status checker.
func (r *Server) StartStatusCheck() {
	// Initialize the last changed timestamp
	r.hasStatusChanged()

	r.statusDone = make(chan int)
	go func() {
		for {
			select {
			case <-r.statusDone:
				return
			case <-time.After(time.Minute):
				if r.hasStatusChanged() {
					r.flushCache()
				}
			}
		}
	}()
}

// StopStatusCheck stops the periodic status checker.
func (r *Server) StopStatusCheck() {
	close(r.statusDone)
}

// SyncProjects updates the list of projects in RBAC
func (r *Server) SyncProjects() error {
	if r.ProjectsFunc == nil {
		return fmt.Errorf("ProjectsFunc isn't configured yet, cannot sync")
	}

	resources := []rbacResource{}
	resourcesMap := map[string]string{}

	// Get all projects
	projects, err := r.ProjectsFunc()
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
func (r *Server) AddProject(id int64, name string) error {
	resource := rbacResource{
		Name:       name,
		Identifier: strconv.FormatInt(id, 10),
	}

	// Update RBAC
	err := r.postResources([]rbacResource{resource}, nil, false)
	if err != nil {
		return err
	}

	// Update project map
	r.resourcesLock.Lock()
	r.resources[name] = strconv.FormatInt(id, 10)
	r.resourcesLock.Unlock()

	return nil
}

// DeleteProject adds a new project resource to RBAC.
func (r *Server) DeleteProject(id int64) error {
	// Update RBAC
	err := r.postResources(nil, []string{strconv.FormatInt(id, 10)}, false)
	if err != nil {
		return err
	}

	// Update project map
	r.resourcesLock.Lock()
	for k, v := range r.resources {
		if v == strconv.FormatInt(id, 10) {
			delete(r.resources, k)
			break
		}
	}
	r.resourcesLock.Unlock()

	return nil
}

// RenameProject renames an existing project resource in RBAC.
func (r *Server) RenameProject(id int64, name string) error {
	return r.AddProject(id, name)
}

// IsAdmin returns whether or not the provided user is an admin.
func (r *Server) IsAdmin(username string) bool {
	r.permissionsLock.Lock()
	defer r.permissionsLock.Unlock()

	// Check whether the permissions are cached
	_, cached := r.permissions[username]

	if !cached {
		r.syncPermissions(username)
	}

	return shared.StringInSlice("admin", r.permissions[username][""])
}

// HasPermission returns whether or not the user has the permission to perform a certain task.
func (r *Server) HasPermission(username, project, permission string) bool {
	r.permissionsLock.Lock()
	defer r.permissionsLock.Unlock()

	// Check whether the permissions are cached
	_, cached := r.permissions[username]

	if !cached {
		r.syncPermissions(username)
	}

	r.resourcesLock.Lock()
	permissions := r.permissions[username][r.resources[project]]
	r.resourcesLock.Unlock()

	return shared.StringInSlice(permission, permissions)
}

func (r *Server) hasStatusChanged() bool {
	var status rbacStatus

	u, err := url.Parse(r.apiURL)
	if err != nil {
		return true
	}

	u.Path = path.Join(u.Path, "/api/service/v1/status")

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return true
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&status)
	if err != nil {
		return true
	}

	if r.lastChange.IsZero() {
		r.lastChange = status.LastChange
		return true
	}

	hasChanged := !r.lastChange.Equal(status.LastChange)
	r.lastChange = status.LastChange

	return hasChanged
}

func (r *Server) flushCache() {
	r.permissionsLock.Lock()
	defer r.permissionsLock.Unlock()

	if len(r.permissions) == 0 {
		return
	}

	logger.Info("Flushing RBAC permissions cache")

	for k, v := range r.permissions {
		for k := range v {
			delete(v, k)
		}

		delete(r.permissions, k)
	}

	logger.Info("Flushed RBAC permissions cache")
}

func (r *Server) syncAdmin(username string) bool {
	u, err := url.Parse(r.apiURL)
	if err != nil {
		return false
	}

	u.Path = path.Join(u.Path, fmt.Sprintf("api/service/v1/resources/lxd/permissions-for-user?u=%s", username))

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return false
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var permissions map[string][]string

	err = json.NewDecoder(resp.Body).Decode(&permissions)
	if err != nil {
		return false
	}

	return shared.StringInSlice("admin", permissions[""])
}

func (r *Server) syncPermissions(username string) error {
	u, err := url.Parse(r.apiURL)
	if err != nil {
		return err
	}

	u.Path = path.Join(u.Path, fmt.Sprintf("/api/service/v1/resources/project/permissions-for-user?u=%s", username))

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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

func (r *Server) postResources(updates []rbacResource, removals []string, force bool) error {
	// Make sure that we have a baseline sync in place
	if !force && r.lastSyncID == "" {
		return r.SyncProjects()
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
	defer resp.Body.Close()

	// Handle errors
	if resp.StatusCode == 409 {
		// Sync IDs don't match, force sync
		return r.SyncProjects()
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
