package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
)

// Store a map of project ID to project name.
var projectsMu = sync.Mutex{}
var projects = make(map[string]string)

// Store a map of users by username, with each value being a map of project ID to list of permissions on that project.
// This is with the exception of "admin", which is keyed as an empty string (e.g. `{"admin-user":{"": ["admin"]}}`).
var permissionsMu = sync.Mutex{}
var permissions = make(map[string]map[string][]string)

// Channel for changes. Should be used to indicate a change on our side when a users permissions are updated or when a
// new project is added.
var change = make(chan struct{})

// syncID indicates to LXD if it is in sync with the RBAC server.
var syncID uint32 = 1

func init() {
	atomic.StoreUint32(&syncID, syncID)
}

// On server change increment the sync ID and send to change channel.
func rbacUpdate() {
	atomic.AddUint32(&syncID, 1)
	change <- struct{}{}
}

func setRBACHandlers(mux *http.ServeMux) {
	for path, handler := range rbacHandlers {
		mux.HandleFunc(path, handler)
	}
}

var rbacHandlers = map[string]http.HandlerFunc{
	"/api/service/v1/resources/project":                      rbacResources,
	"/api/service/v1/changes":                                rbacChanges,
	"/api/service/v1/resources/lxd/permissions-for-user":     rbacPermissions,
	"/api/service/v1/resources/project/permissions-for-user": rbacPermissions,
	"/set-perms": rbacSetPermissions,
}

// This is continuously polled by LXD. If the request times out it will just poll again.
func rbacChanges(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Long poll for changes.
	<-change

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	resp := fmt.Sprintf(`{"last-change": "%d"}`, atomic.LoadUint32(&syncID))
	_, _ = writer.Write([]byte(resp))
}

// This is the POST endpoint LXD calls when adding/removing projects.
func rbacResources(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type requestBody struct {
		LastSyncID string `json:"last-sync-id"`
		Updates    []struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"updates"`
		Removals []string `json:"removals"`
	}

	var body requestBody
	err := json.NewDecoder(request.Body).Decode(&body)
	if err != nil {
		http.Error(writer, fmt.Sprintf("Failed to unmarshal request body: %v", err), http.StatusBadRequest)
		return
	}

	projectsMu.Lock()
	defer projectsMu.Unlock()

	for _, update := range body.Updates {
		projects[update.Identifier] = update.Name
	}

	for _, removal := range body.Removals {
		delete(projects, removal)
	}

	rbacUpdate()
	writer.WriteHeader(http.StatusOK)
	resp := fmt.Sprintf(`{"sync-id": "%d"}`, atomic.LoadUint32(&syncID))
	_, _ = writer.Write([]byte(resp))
}

// This endpoint returns the permissions for the user given by the "u" query parameter. It is used for both admin and project
// permissions because we're storing all permissions in a single map.
func rbacPermissions(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		http.Error(writer, fmt.Sprintf("Failed to parse query parameters: %v", err), http.StatusBadRequest)
		return
	}

	user := values.Get("u")

	permissionsMu.Lock()
	defer permissionsMu.Unlock()

	userPermissions := permissions[user]
	b, err := json.Marshal(userPermissions)
	if err != nil {
		http.Error(writer, fmt.Sprintf("Failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(b)
}

// This is the only endpoint that is not present on the RBAC server. It is here so we can manipulate permissions of a user
// at runtime for testing purposes.
//
// Examples:
// - Set admin permissions for `user1`: `curl -X POST curl "<server_address>/set-perms?user=user1" -X POST -d '{"": ["admin"]}'`.
// - Set view permissions for `user1` on project default: `curl -X POST curl "<server_address>/set-perms?user=user1" -X POST -d '{"default": ["view"]}'`.
func rbacSetPermissions(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		http.Error(writer, fmt.Sprintf("Failed to parse query parameters: %v", err), http.StatusBadRequest)
		return
	}

	user := values.Get("user")

	newPermissions := map[string][]string{}
	err = json.NewDecoder(request.Body).Decode(&newPermissions)
	if err != nil {
		http.Error(writer, fmt.Sprintf("Failed to unmarshal request body: %v", err), http.StatusBadRequest)
		return
	}

	projectsMu.Lock()
	defer projectsMu.Unlock()

	// Need to translate project name to project ID for setting in the permissions struct.
	translatedPermissions := map[string][]string{}
	for projectName, perms := range newPermissions {
		if projectName == "" {
			translatedPermissions[""] = perms
			continue
		}

		var projectID string
		for k, v := range projects {
			if v == projectName {
				projectID = k
			}
		}

		if projectID == "" {
			http.Error(writer, "Project not found", http.StatusNotFound)
			return
		}

		translatedPermissions[projectID] = perms
	}

	permissionsMu.Lock()
	defer permissionsMu.Unlock()

	permissions[user] = translatedPermissions
	rbacUpdate()
	writer.WriteHeader(http.StatusOK)
}
