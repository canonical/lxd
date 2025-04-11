package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/cloudinit"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
	"github.com/canonical/lxd/shared/ws"
)

// DevLXDSecurityKey are instance configuration keys used to enable devLXD features.
type DevLXDSecurityKey string

const (
	// The security.devlxd key is used to enable devLXD for an instance.
	devLXDSecurityKey DevLXDSecurityKey = "security.devlxd"

	// The security.devlxd.images key is used to enable devLXD image export.
	devLXDSecurityImagesKey DevLXDSecurityKey = "security.devlxd.images"
)

// devLXDAPIHandlerFunc is a function that handles requests to the DevLXD API.
type devLXDAPIHandlerFunc func(*Daemon, instance.Instance, *http.Request) response.Response

// hoistFunc is a function that wraps the incoming requests, retrieves the targeted instance, and passes
// it to the handler.
type hoistFunc func(*Daemon, *http.Request, devLXDAPIHandlerFunc) response.Response

// devLXDAPIEndpointAction represents an action on an devLXD API endpoint.
type devLXDAPIEndpointAction struct {
	Handler devLXDAPIHandlerFunc
}

// devLXDAPIEndpoint represents a URL in devLXD API.
type devLXDAPIEndpoint struct {
	Name   string // Name for this endpoint.
	Path   string // Path pattern for this endpoint
	Get    devLXDAPIEndpointAction
	Head   devLXDAPIEndpointAction
	Put    devLXDAPIEndpointAction
	Post   devLXDAPIEndpointAction
	Delete devLXDAPIEndpointAction
	Patch  devLXDAPIEndpointAction
}

var apiDevLXD = []devLXDAPIEndpoint{
	{
		Path: "/",
		Get: devLXDAPIEndpointAction{
			Handler: func(d *Daemon, c instance.Instance, r *http.Request) response.Response {
				return response.DevLXDResponse(http.StatusOK, []string{"/1.0"}, "json", c.Type() == instancetype.VM)
			},
		},
	},
	devLXD10Endpoint,
	devLXDConfigEndpoint,
	devLXDConfigKeyEndpoint,
	devLXDImageExportEndpoint,
	devLXDMetadataEndpoint,
	devLXDEventsEndpoint,
	devLXDDevicesEndpoint,
	devLXDUbuntuProEndpoint,
	devLXDUbuntuProTokenEndpoint,
}

var devLXD10Endpoint = devLXDAPIEndpoint{
	Path:  "",
	Get:   devLXDAPIEndpointAction{Handler: devLXDAPIGetHandler},
	Patch: devLXDAPIEndpointAction{Handler: devLXDAPIPatchHandler},
}

func devLXDAPIGetHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	var location string

	if d.serverClustered {
		location = c.Location()
	} else {
		var err error

		location, err = os.Hostname()
		if err != nil {
			return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}
	}

	var state api.StatusCode

	if shared.IsTrue(c.LocalConfig()["volatile.last_state.ready"]) {
		state = api.Ready
	} else {
		state = api.Started
	}

	return response.DevLXDResponse(http.StatusOK, api.DevLXDGet{APIVersion: version.APIVersion, Location: location, InstanceType: c.Type().String(), DevLXDPut: api.DevLXDPut{State: state.String()}}, "json", c.Type() == instancetype.VM)
}

func devLXDAPIPatchHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	s := d.State()

	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	req := api.DevLXDPut{}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid request body: %w", err), c.Type() == instancetype.VM)
	}

	state := api.StatusCodeFromString(req.State)

	if state != api.Started && state != api.Ready {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid state %q", req.State), c.Type() == instancetype.VM)
	}

	err = c.VolatileSet(map[string]string{"volatile.last_state.ready": strconv.FormatBool(state == api.Ready)})
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Failed to set instance state: %w", err), c.Type() == instancetype.VM)
	}

	if state == api.Ready {
		s.Events.SendLifecycle(c.Project().Name, lifecycle.InstanceReady.Event(c, nil))
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw", c.Type() == instancetype.VM)
}

var devLXDConfigEndpoint = devLXDAPIEndpoint{
	Path: "config",
	Get:  devLXDAPIEndpointAction{Handler: devLXDConfigGetHandler},
}

func devLXDConfigGetHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	filtered := []string{}
	hasSSHKeys := false
	hasVendorData := false
	hasUserData := false
	for k := range c.ExpandedConfig() {
		if !(strings.HasPrefix(k, "user.") || strings.HasPrefix(k, "cloud-init.")) {
			continue
		}

		if strings.HasPrefix(k, "cloud-init.ssh-keys.") {
			// cloud-init.ssh-keys keys are not to be retrieved by cloud-init directly, but instead LXD converts them
			// into cloud-init config and merges it into cloud-init.[vendor|user]-data.
			// This way we can make use of the full array of options proivded by cloud-config for injecting keys
			// and not compromise any cloud-init config defined on the instance's expanded config.
			hasSSHKeys = true
			continue
		}

		if shared.ValueInSlice(k, cloudinit.VendorDataKeys) {
			hasVendorData = true
		} else if shared.ValueInSlice(k, cloudinit.UserDataKeys) {
			hasUserData = true
		}

		filtered = append(filtered, "/1.0/config/"+k)
	}

	// If [vendor|user]-data are not defined, cloud-init should still request for them if there are SSH keys defined via
	// "cloud-init.ssh.keys". Use both user.* and cloud-init.* for compatibitily with older cloud-init.
	if hasSSHKeys && !hasVendorData {
		filtered = append(filtered, "/1.0/config/cloud-init.vendor-data")
		filtered = append(filtered, "/1.0/config/user.vendor-data")
	}

	if hasSSHKeys && !hasUserData {
		filtered = append(filtered, "/1.0/config/cloud-init.user-data")
		filtered = append(filtered, "/1.0/config/user.user-data")
	}

	return response.DevLXDResponse(http.StatusOK, filtered, "json", c.Type() == instancetype.VM)
}

var devLXDConfigKeyEndpoint = devLXDAPIEndpoint{
	Path: "config/{key}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDConfigKeyGetHandler},
}

func devLXDConfigKeyGetHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	key, err := url.PathUnescape(mux.Vars(r)["key"])
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusBadRequest, "bad request"), c.Type() == instancetype.VM)
	}

	if !strings.HasPrefix(key, "user.") && !strings.HasPrefix(key, "cloud-init.") {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	var value string

	isVendorDataKey := shared.ValueInSlice(key, cloudinit.VendorDataKeys)
	isUserDataKey := shared.ValueInSlice(key, cloudinit.UserDataKeys)

	// For values containing cloud-init seed data, try to merge into them additional SSH keys present on the instance config.
	// If parsing the config is not possible, abstain from merging the additional keys.
	if isVendorDataKey || isUserDataKey {
		cloudInitData := cloudinit.GetEffectiveConfig(c.ExpandedConfig(), key, c.Name(), c.Project().Name)
		if isVendorDataKey {
			value = cloudInitData.VendorData
		} else {
			value = cloudInitData.UserData
		}
	} else {
		value = c.ExpandedConfig()[key]
	}

	// If the resulting value is empty, return Not Found.
	if value == "" {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusNotFound, "not found"), c.Type() == instancetype.VM)
	}

	return response.DevLXDResponse(http.StatusOK, value, "raw", c.Type() == instancetype.VM)
}

var devLXDImageExportEndpoint = devLXDAPIEndpoint{
	Path: "images/{fingerprint}/export",
	Get:  devLXDAPIEndpointAction{Handler: devLXDImageExportHandler},
}

func devLXDImageExportHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	if shared.IsFalseOrEmpty(c.ExpandedConfig()["security.devlxd.images"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	return imageExport(d, r)
}

var devLXDMetadataEndpoint = devLXDAPIEndpoint{
	Path: "meta-data",
	Get:  devLXDAPIEndpointAction{Handler: devLXDMetadataGetHandler},
}

func devLXDMetadataGetHandler(d *Daemon, inst instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(inst.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), inst.Type() == instancetype.VM)
	}

	value := inst.ExpandedConfig()["user.meta-data"]
	return response.DevLXDResponse(http.StatusOK, "instance-id: "+inst.CloudInitID()+"\nlocal-hostname: "+inst.Name()+"\n"+value, "raw", inst.Type() == instancetype.VM)
}

var devLXDEventsEndpoint = devLXDAPIEndpoint{
	Path: "events",
	Get:  devLXDAPIEndpointAction{Handler: devLXDEventsGetHandler},
}

func devLXDEventsGetHandler(d *Daemon, inst instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(inst.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), inst.Type() == instancetype.VM)
	}

	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "config,device"
	}

	// Wrap into manual response because http writer is required to stream the event to the client.
	return response.ManualResponse(func(w http.ResponseWriter) error {
		var listenerConnection events.EventListenerConnection
		var resp response.Response

		// If the client has not requested a websocket connection then fallback to long polling event stream mode.
		if r.Header.Get("Upgrade") == "websocket" {
			conn, err := ws.Upgrader.Upgrade(w, r, nil)
			if err != nil {
				return api.StatusErrorf(http.StatusInternalServerError, "internal server error")
			}

			defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

			listenerConnection = events.NewWebsocketListenerConnection(conn)

			resp = response.DevLXDResponse(http.StatusOK, "websocket", "websocket", inst.Type() == instancetype.VM)
		} else {
			h, ok := w.(http.Hijacker)
			if !ok {
				return api.StatusErrorf(http.StatusInternalServerError, "internal server error")
			}

			conn, _, err := h.Hijack()
			if err != nil {
				return api.StatusErrorf(http.StatusInternalServerError, "internal server error")
			}

			defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

			listenerConnection, err = events.NewStreamListenerConnection(conn)
			if err != nil {
				return api.StatusErrorf(http.StatusInternalServerError, "internal server error")
			}

			resp = response.DevLXDResponse(http.StatusOK, "", "raw", inst.Type() == instancetype.VM)
		}

		listener, err := d.State().DevlxdEvents.AddListener(inst.ID(), listenerConnection, strings.Split(typeStr, ","))
		if err != nil {
			return api.StatusErrorf(http.StatusInternalServerError, "internal server error")
		}

		logger.Debug("New container event listener", logger.Ctx{"instance": inst.Name(), "project": inst.Project().Name, "listener_id": listener.ID})
		listener.Wait(r.Context())

		return resp.Render(w, r)
	})
}

var devLXDDevicesEndpoint = devLXDAPIEndpoint{
	Path: "devices",
	Get:  devLXDAPIEndpointAction{Handler: devLXDDevicesGetHandler},
}

func devLXDDevicesGetHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	// Populate NIC hwaddr from volatile if not explicitly specified.
	// This is so cloud-init running inside the instance can identify the NIC when the interface name is
	// different than the LXD device name (such as when run inside a VM).
	localConfig := c.LocalConfig()
	devices := c.ExpandedDevices()
	for devName, devConfig := range devices {
		if devConfig["type"] == "nic" && devConfig["hwaddr"] == "" && localConfig["volatile."+devName+".hwaddr"] != "" {
			devices[devName]["hwaddr"] = localConfig["volatile."+devName+".hwaddr"]
		}
	}

	return response.DevLXDResponse(http.StatusOK, c.ExpandedDevices(), "json", c.Type() == instancetype.VM)
}

var devLXDUbuntuProEndpoint = devLXDAPIEndpoint{
	Path: "ubuntu-pro",
	Get:  devLXDAPIEndpointAction{Handler: devLXDUbuntuProGetHandler},
}

func devLXDUbuntuProGetHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusForbidden), c.Type() == instancetype.VM)
	}

	if r.Method != http.MethodGet {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusMethodNotAllowed), c.Type() == instancetype.VM)
	}

	settings := d.State().UbuntuPro.GuestAttachSettings(c.ExpandedConfig()["ubuntu_pro.guest_attach"])

	// Otherwise, return the value from the instance configuration.
	return response.DevLXDResponse(http.StatusOK, settings, "json", c.Type() == instancetype.VM)
}

var devLXDUbuntuProTokenEndpoint = devLXDAPIEndpoint{
	Path: "ubuntu-pro/token",
	Post: devLXDAPIEndpointAction{Handler: devLXDUbuntuProTokenPostHandler},
}

func devLXDUbuntuProTokenPostHandler(d *Daemon, c instance.Instance, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusForbidden), c.Type() == instancetype.VM)
	}

	if r.Method != http.MethodPost {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusMethodNotAllowed), c.Type() == instancetype.VM)
	}

	// Return http.StatusForbidden if the host does not have guest attachment enabled.
	tokenJSON, err := d.State().UbuntuPro.GetGuestToken(r.Context(), c.ExpandedConfig()["ubuntu_pro.guest_attach"])
	if err != nil {
		return response.DevLXDErrorResponse(fmt.Errorf("Failed to get an Ubuntu Pro guest token: %w", err), c.Type() == instancetype.VM)
	}

	// Pass it back to the guest.
	return response.DevLXDResponse(http.StatusOK, tokenJSON, "json", c.Type() == instancetype.VM)
}

func devLXDAPI(d *Daemon, f hoistFunc, rawResponse bool) http.Handler {
	m := mux.NewRouter()
	m.UseEncodedPath() // Allow encoded values in path segments.

	for _, handler := range apiDevLXD {
		registerDevLXDEndpoint(d, m, "1.0", handler, f, rawResponse)
	}

	return m
}

func registerDevLXDEndpoint(d *Daemon, apiRouter *mux.Router, apiVersion string, ep devLXDAPIEndpoint, f hoistFunc, rawResponse bool) {
	uri := ep.Path
	if uri != "/" {
		uri = path.Join("/", apiVersion, ep.Path)
	}

	// Function that handles the request by calling the appropriate handler.
	handleFunc := func(w http.ResponseWriter, r *http.Request) {
		handleRequest := func(action devLXDAPIEndpointAction) response.Response {
			if action.Handler == nil {
				return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusNotImplemented), rawResponse)
			}

			return f(d, r, action.Handler)
		}

		var resp response.Response

		switch r.Method {
		case http.MethodHead:
			resp = handleRequest(ep.Head)
		case http.MethodGet:
			resp = handleRequest(ep.Get)
		case http.MethodPost:
			resp = handleRequest(ep.Post)
		case http.MethodPut:
			resp = handleRequest(ep.Put)
		case http.MethodPatch:
			resp = handleRequest(ep.Patch)
		case http.MethodDelete:
			resp = handleRequest(ep.Delete)
		default:
			resp = response.DevLXDErrorResponse(api.StatusErrorf(http.StatusNotFound, "Method %q not found", r.Method), rawResponse)
		}

		// Write response and handle errors.
		err := resp.Render(w, r)
		if err != nil {
			writeErr := response.DevLXDErrorResponse(err, rawResponse).Render(w, r)
			if writeErr != nil {
				logger.Warn("Failed writing error for HTTP response", logger.Ctx{"url": uri, "err": err, "writeErr": writeErr})
			}
		}
	}

	route := apiRouter.HandleFunc(uri, handleFunc)

	// If the endpoint has a canonical name then record it so it can be used to build URLS
	// and accessed in the context of the request by the handler function.
	if ep.Name != "" {
		route.Name(ep.Name)
	}
}
