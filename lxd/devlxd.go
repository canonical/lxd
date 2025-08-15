package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/cloudinit"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
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
type devLXDAPIHandlerFunc func(*Daemon, *http.Request) response.Response

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
			Handler: func(d *Daemon, r *http.Request) response.Response {
				return response.DevLXDResponse(http.StatusOK, []string{"/1.0"}, "json")
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

func devLXDAPIGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context())
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	var location string

	if d.serverClustered {
		location = inst.Location()
	} else {
		var err error

		location, err = os.Hostname()
		if err != nil {
			return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"))
		}
	}

	var state api.StatusCode

	if shared.IsTrue(inst.LocalConfig()["volatile.last_state.ready"]) {
		state = api.Ready
	} else {
		state = api.Started
	}

	resp := api.DevLXDGet{
		APIVersion:   version.APIVersion,
		Location:     location,
		InstanceType: inst.Type().String(),
		DevLXDPut: api.DevLXDPut{
			State: state.String(),
		},
	}

	return response.DevLXDResponse(http.StatusOK, resp, "json")
}

func devLXDAPIPatchHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	s := d.State()

	req := api.DevLXDPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid request body: %w", err))
	}

	state := api.StatusCodeFromString(req.State)

	if state != api.Started && state != api.Ready {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid state %q", req.State))
	}

	err = inst.VolatileSet(map[string]string{"volatile.last_state.ready": strconv.FormatBool(state == api.Ready)})
	if err != nil {
		return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Failed to set instance state: %w", err))
	}

	if state == api.Ready {
		s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceReady.Event(inst, nil))
	}

	return response.DevLXDResponse(http.StatusOK, "", "raw")
}

var devLXDConfigEndpoint = devLXDAPIEndpoint{
	Path: "config",
	Get:  devLXDAPIEndpointAction{Handler: devLXDConfigGetHandler},
}

func devLXDConfigGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	filtered := []string{}
	hasSSHKeys := false
	hasVendorData := false
	hasUserData := false
	for k := range inst.ExpandedConfig() {
		if !strings.HasPrefix(k, "user.") && !strings.HasPrefix(k, "cloud-init.") {
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

		if slices.Contains(cloudinit.VendorDataKeys, k) {
			hasVendorData = true
		} else if slices.Contains(cloudinit.UserDataKeys, k) {
			hasUserData = true
		}

		filtered = append(filtered, "/1.0/config/"+k)
	}

	// If [vendor|user]-data are not defined, cloud-init should still request for them if there are SSH keys defined via
	// "cloud-init.ssh.keys". Use both user.* and cloud-init.* for compatibitily with older cloud-init.
	if hasSSHKeys && !hasVendorData {
		filtered = append(filtered, "/1.0/config/cloud-init.vendor-data", "/1.0/config/user.vendor-data")
	}

	if hasSSHKeys && !hasUserData {
		filtered = append(filtered, "/1.0/config/cloud-init.user-data", "/1.0/config/user.user-data")
	}

	return response.DevLXDResponse(http.StatusOK, filtered, "json")
}

var devLXDConfigKeyEndpoint = devLXDAPIEndpoint{
	Path: "config/{key}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDConfigKeyGetHandler},
}

func devLXDConfigKeyGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	key, err := url.PathUnescape(mux.Vars(r)["key"])
	if err != nil {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusBadRequest))
	}

	if !strings.HasPrefix(key, "user.") && !strings.HasPrefix(key, "cloud-init.") {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusForbidden))
	}

	var value string

	isVendorDataKey := slices.Contains(cloudinit.VendorDataKeys, key)
	isUserDataKey := slices.Contains(cloudinit.UserDataKeys, key)

	// For values containing cloud-init seed data, try to merge into them additional SSH keys present on the instance config.
	// If parsing the config is not possible, abstain from merging the additional keys.
	if isVendorDataKey || isUserDataKey {
		cloudInitData := cloudinit.GetEffectiveConfig(inst.ExpandedConfig(), key, inst.Name(), inst.Project().Name)
		if isVendorDataKey {
			value = cloudInitData.VendorData
		} else {
			value = cloudInitData.UserData
		}
	} else {
		value = inst.ExpandedConfig()[key]
	}

	// If the resulting value is empty, return Not Found.
	if value == "" {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusNotFound))
	}

	return response.DevLXDResponse(http.StatusOK, value, "raw")
}

var devLXDImageExportEndpoint = devLXDAPIEndpoint{
	Path: "images/{fingerprint}/export",
	Get:  devLXDAPIEndpointAction{Handler: devLXDImageExportHandler},
}

// devLXDImageExportHandler returns a file response containing the image files. The requested fingerprint must match
// exactly, and the project must be "default". Images are only made available over DevLXD if they are public or cached.
//
// Note: This endpoint used to call into the image export handler directly, it therefore returns full API responses
// rather than DevLXDErrorResponses for compatibility.
func devLXDImageExportHandler(d *Daemon, r *http.Request) response.Response {
	_, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey, devLXDSecurityImagesKey)
	if err != nil {
		// XXX: The imageExport returns a non-devLXD error response which is
		// inconsistent with the rest of the devLXD API. This is because the response
		// from the LXD API handler (imageExport) is called directly. This means that
		// also the error responses will be returned in non-devLXD format.
		//
		// To make responses consistent and easy to parse on the client side, while reducing
		// the impact of breaking changes, we return LXD API response error here as an exception.
		return response.Forbidden(err)
	}

	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	if projectName != api.ProjectDefaultName {
		// Disallow requests made to non-default projects.
		return response.NotFound(nil)
	}

	s := d.State()

	var imgInfo *api.Image
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Perform exact match on image fingerprint and project.
		dbImage, err := cluster.GetImage(ctx, tx.Tx(), api.ProjectDefaultName, fingerprint)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return err
		}

		// Access check now to avoid further db calls if not allowed.
		if dbImage == nil || (!dbImage.Cached && !dbImage.Public) {
			return api.NewGenericStatusError(http.StatusNotFound)
		}

		// Expand image for call to imageExportFiles.
		imgInfo, err = dbImage.ToAPI(ctx, tx.Tx(), api.ProjectDefaultName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return imageExportFiles(r.Context(), s, imgInfo, projectName)
}

var devLXDMetadataEndpoint = devLXDAPIEndpoint{
	Path: "meta-data",
	Get:  devLXDAPIEndpointAction{Handler: devLXDMetadataGetHandler},
}

func devLXDMetadataGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	meta := inst.ExpandedConfig()["user.meta-data"]
	resp := "instance-id: " + inst.CloudInitID() + "\nlocal-hostname: " + inst.Name() + "\n" + meta
	return response.DevLXDResponse(http.StatusOK, resp, "raw")
}

var devLXDEventsEndpoint = devLXDAPIEndpoint{
	Path: "events",
	Get:  devLXDAPIEndpointAction{Handler: devLXDEventsGetHandler},
}

func devLXDEventsGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
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

			resp = response.DevLXDResponse(http.StatusOK, "websocket", "websocket")
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

			resp = response.DevLXDResponse(http.StatusOK, "", "raw")
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

func devLXDDevicesGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Populate NIC hwaddr from volatile if not explicitly specified.
	// This is so cloud-init running inside the instance can identify the NIC when the interface name is
	// different than the LXD device name (such as when run inside a VM).
	localConfig := inst.LocalConfig()
	devices := inst.ExpandedDevices()
	for devName, devConfig := range devices {
		if devConfig["type"] == "nic" && devConfig["hwaddr"] == "" && localConfig["volatile."+devName+".hwaddr"] != "" {
			devices[devName]["hwaddr"] = localConfig["volatile."+devName+".hwaddr"]
		}
	}

	return response.DevLXDResponse(http.StatusOK, inst.ExpandedDevices(), "json")
}

var devLXDUbuntuProEndpoint = devLXDAPIEndpoint{
	Path: "ubuntu-pro",
	Get:  devLXDAPIEndpointAction{Handler: devLXDUbuntuProGetHandler},
}

func devLXDUbuntuProGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	settings := d.State().UbuntuPro.GuestAttachSettings(inst.ExpandedConfig()["ubuntu_pro.guest_attach"])

	// Otherwise, return the value from the instance configuration.
	return response.DevLXDResponse(http.StatusOK, settings, "json")
}

var devLXDUbuntuProTokenEndpoint = devLXDAPIEndpoint{
	Path: "ubuntu-pro/token",
	Post: devLXDAPIEndpointAction{Handler: devLXDUbuntuProTokenPostHandler},
}

func devLXDUbuntuProTokenPostHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	// Return http.StatusForbidden if the host does not have guest attachment enabled.
	tokenJSON, err := d.State().UbuntuPro.GetGuestToken(r.Context(), inst.ExpandedConfig()["ubuntu_pro.guest_attach"])
	if err != nil {
		return response.DevLXDErrorResponse(fmt.Errorf("Failed to get an Ubuntu Pro guest token: %w", err))
	}

	// Pass it back to the guest.
	return response.DevLXDResponse(http.StatusOK, tokenJSON, "json")
}

func devLXDAPI(d *Daemon, f hoistFunc, isVsock bool) http.Handler {
	m := mux.NewRouter()
	m.UseEncodedPath() // Allow encoded values in path segments.

	for _, handler := range apiDevLXD {
		registerDevLXDEndpoint(d, m, "1.0", handler, f, isVsock)
	}

	return m
}

func registerDevLXDEndpoint(d *Daemon, apiRouter *mux.Router, apiVersion string, ep devLXDAPIEndpoint, f hoistFunc, isVsock bool) {
	uri := ep.Path
	if uri != "/" {
		uri = path.Join("/", apiVersion, ep.Path)
	}

	// Function that handles the request by calling the appropriate handler.
	handleFunc := func(w http.ResponseWriter, r *http.Request) {
		// Initialise the request context info.
		reqInfo := request.InitContextInfo(r)

		// Set devLXD auth method to identify this request as coming from the /dev/lxd socket.
		reqInfo.Protocol = request.ProtocolDevLXD

		// Indicate whether the devLXD is being accessed over vsock. This allowes the handler
		// to determine the correct response type. The responses over vsock are always
		// in api.Response format, while the responses over Unix socket are in devLXDResponse format.
		request.SetContextValue(r, request.CtxDevLXDOverVsock, isVsock)

		handleRequest := func(action devLXDAPIEndpointAction) (resp response.Response) {
			// Handle panic in the handler.
			defer func() {
				err := recover()
				if err != nil {
					logger.Error("Panic in devLXD API handler", logger.Ctx{"err": err})
					resp = response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "%v", err))
				}
			}()

			// Verify handler.
			if action.Handler == nil {
				return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusNotImplemented))
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
			resp = response.DevLXDErrorResponse(api.StatusErrorf(http.StatusNotFound, "Method %q not found", r.Method))
		}

		// Write response and handle errors.
		err := resp.Render(w, r)
		if err != nil {
			writeErr := response.DevLXDErrorResponse(err).Render(w, r)
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

// getInstanceFromContextAndCheckSecurityFlags checks if the instance has the provided devLXD security features enabled.
func getInstanceFromContextAndCheckSecurityFlags(ctx context.Context, keys ...DevLXDSecurityKey) (instance.Instance, error) {
	inst, err := request.GetContextValue[instance.Instance](ctx, request.CtxDevLXDInstance)
	if err != nil {
		return nil, err
	}

	config := inst.ExpandedConfig()
	for _, key := range keys {
		value := config[string(key)]

		// The devLXD is enabled by default, therefore we only prevent access if the feature
		// is explicitly disabled (set to "false"). All other features must be explicitly enabled.
		if shared.IsFalse(value) || (value == "" && key != devLXDSecurityKey) {
			return nil, api.NewGenericStatusError(http.StatusForbidden)
		}
	}

	return inst, nil
}
