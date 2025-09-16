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

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/auth/bearer"
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
	"github.com/canonical/lxd/shared/entity"
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

	// The security.devlxd.management.volumes key is used to allow volume
	// management through devLXD.
	devLXDSecurityManagementVolumesKey DevLXDSecurityKey = "security.devlxd.management.volumes"
)

// devLXDAPIAuthenticator is an interface that abstracts the authentication mechanism used to
// authenticate the instance making the /dev/lxd request.
type devLXDAuthenticator interface {
	IsVsock() bool
	AuthenticateInstance(*Daemon, *http.Request) (instance.Instance, error)
}

var apiDevLXD = []APIEndpoint{
	{
		Path: "/",
		Get: APIEndpointAction{
			Handler: func(d *Daemon, r *http.Request) response.Response {
				return response.DevLXDResponse(http.StatusOK, []string{"/1.0"}, "json")
			},
			AllowUntrusted: true,
		},
	},
	devLXD10Endpoint,
	devLXDConfigEndpoint,
	devLXDConfigKeyEndpoint,
	devLXDImageExportEndpoint,
	devLXDMetadataEndpoint,
	devLXDEventsEndpoint,
	devLXDDevicesEndpoint,
	devLXDInstanceEndpoint,
	devLXDStoragePoolEndpoint,
	devLXDStoragePoolVolumeTypeEndpoint,
	devLXDStoragePoolVolumesEndpoint,
	devLXDStoragePoolVolumesTypeEndpoint,
	devLXDUbuntuProEndpoint,
	devLXDUbuntuProTokenEndpoint,
}

var devLXD10Endpoint = APIEndpoint{
	Path:  "",
	Get:   APIEndpointAction{Handler: devLXDAPIGetHandler, AllowUntrusted: true},
	Patch: APIEndpointAction{Handler: devLXDAPIPatchHandler, AllowUntrusted: true},
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

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	clientAuth := api.AuthUntrusted
	if requestor.IsTrusted() {
		clientAuth = api.AuthTrusted
	}

	supportedStorageDrivers := []api.DevLXDServerStorageDriverInfo{}

	// Include supported storage drivers if the instance has the devLXD volume
	// management security flag enabled.
	if shared.IsTrue(inst.ExpandedConfig()[string(devLXDSecurityManagementVolumesKey)]) {
		storageDrivers, _ := readStoragePoolDriversCache()
		for _, driver := range storageDrivers {
			supportedStorageDrivers = append(supportedStorageDrivers, api.DevLXDServerStorageDriverInfo{
				Name:   driver.Name,
				Remote: driver.Remote,
			})
		}
	}

	resp := api.DevLXDGet{
		APIVersion:              version.APIVersion,
		Location:                location,
		InstanceType:            inst.Type().String(),
		Auth:                    clientAuth,
		SupportedStorageDrivers: supportedStorageDrivers,
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

var devLXDConfigEndpoint = APIEndpoint{
	Path: "config",
	Get:  APIEndpointAction{Handler: devLXDConfigGetHandler, AllowUntrusted: true},
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

var devLXDConfigKeyEndpoint = APIEndpoint{
	Path: "config/{key}",
	Get:  APIEndpointAction{Handler: devLXDConfigKeyGetHandler, AllowUntrusted: true},
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

var devLXDImageExportEndpoint = APIEndpoint{
	Path: "images/{fingerprint}/export",
	Get:  APIEndpointAction{Handler: devLXDImageExportHandler, AllowUntrusted: true},
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

var devLXDMetadataEndpoint = APIEndpoint{
	Path: "meta-data",
	Get:  APIEndpointAction{Handler: devLXDMetadataGetHandler, AllowUntrusted: true},
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

var devLXDEventsEndpoint = APIEndpoint{
	Path: "events",
	Get:  APIEndpointAction{Handler: devLXDEventsGetHandler, AllowUntrusted: true},
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

var devLXDDevicesEndpoint = APIEndpoint{
	Path: "devices",
	Get:  APIEndpointAction{Handler: devLXDDevicesGetHandler, AllowUntrusted: true},
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

var devLXDUbuntuProEndpoint = APIEndpoint{
	Path: "ubuntu-pro",
	Get:  APIEndpointAction{Handler: devLXDUbuntuProGetHandler, AllowUntrusted: true},
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

var devLXDUbuntuProTokenEndpoint = APIEndpoint{
	Path: "ubuntu-pro/token",
	Post: APIEndpointAction{Handler: devLXDUbuntuProTokenPostHandler, AllowUntrusted: true},
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

func devLXDAPI(d *Daemon, authenticator devLXDAuthenticator) http.Handler {
	m := mux.NewRouter()
	m.UseEncodedPath() // Allow encoded values in path segments.

	for _, handler := range apiDevLXD {
		registerDevLXDEndpoint(d, m, "1.0", handler, authenticator)
	}

	return m
}

func registerDevLXDEndpoint(d *Daemon, apiRouter *mux.Router, apiVersion string, ep APIEndpoint, authenticator devLXDAuthenticator) {
	uri := ep.Path
	if uri != "/" {
		uri = path.Join("/", apiVersion, ep.Path)
	}

	// Function that handles the request by calling the appropriate handler.
	handleFunc := func(w http.ResponseWriter, r *http.Request) {
		var requestor request.RequestorArgs

		// Indicate whether the devLXD is being accessed over vsock. This allowes the handler
		// to determine the correct response type. The responses over vsock are always
		// in api.Response format, while the responses over Unix socket are in devLXDResponse format.
		request.SetContextValue(r, request.CtxDevLXDOverVsock, authenticator.IsVsock())

		// Set [request.ProtocolDevLXD] by default identify this request as coming from the /dev/lxd socket.
		requestor.Protocol = request.ProtocolDevLXD

		// Check if the caller has a bearer token.
		isBearerRequest, token, subject := bearer.IsDevLXDRequest(r, d.globalConfig.ClusterUUID())
		if isBearerRequest {
			bearerRequestor, err := bearer.Authenticate(token, subject, d.identityCache)
			if err != nil {
				// Deny access to DevLXD altogether if the provided token is not verifiable.
				_ = response.DevLXDErrorResponse(fmt.Errorf("Failed to verify bearer token: %w", err)).Render(w, r)
				return
			}

			requestor = *bearerRequestor
		}

		// Always set [request.ProtocolDevLXD] to identify this request as coming from the /dev/lxd socket.
		requestor.Protocol = request.ProtocolDevLXD

		err := request.SetRequestor(r, d.identityCache, requestor)
		if err != nil {
			_ = response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "%v", err)).Render(w, r)
			return
		}

		inst, err := authenticator.AuthenticateInstance(d, r)
		if err != nil {
			_ = response.DevLXDErrorResponse(err).Render(w, r)
			return
		}

		request.SetContextValue(r, request.CtxDevLXDInstance, inst)

		handleRequest := func(action APIEndpointAction) (resp response.Response) {
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

			// All API endpoint acctions should either have an access handler or allow untrusted requests.
			if action.AccessHandler == nil && !action.AllowUntrusted {
				return response.DevLXDErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Access handler not defined for %s %s", r.Method, r.URL.RequestURI()))
			}

			// If the request is not trusted, only call the handler if the action allows it.
			if !requestor.Trusted && !action.AllowUntrusted {
				return response.DevLXDErrorResponse(api.NewStatusError(http.StatusForbidden, "You must be authenticated"))
			}

			// Call the access handler if there is one.
			if action.AccessHandler != nil {
				resp := action.AccessHandler(d, r)
				if resp != response.EmptySyncResponse {
					return resp
				}
			}

			return action.Handler(d, r)
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
		err = resp.Render(w, r)
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

// enforceDevLXDProject ensures the "project" query parameter matches the instance's project.
// If missing, it is set to the instance's project, since permission checkers use it to identify the project.
// If different, the request is rejected with a forbidden error.
func enforceDevLXDProject(r *http.Request) error {
	inst, err := request.GetContextValue[instance.Instance](r.Context(), request.CtxDevLXDInstance)
	if err != nil {
		return err
	}

	instProject := inst.Project().Name
	projectParam := request.QueryParam(r, "project")

	if projectParam == "" {
		// Ensure the project query parameter is always set.
		// This is needed by the permission checkers to determine the correct project.
		q := r.URL.Query()
		q.Set("project", instProject)
		r.URL.RawQuery = q.Encode()
	} else if projectParam != instProject {
		// Disallow cross-project access.
		return api.NewGenericStatusError(http.StatusForbidden)
	}

	return nil
}

// allowDevLXDAuthenticated is an access handler that rejects requests from unauthenticated clients.
// It is similar to [allowAuthenticated] but returns DevLXD errors.
func allowDevLXDAuthenticated(_ *Daemon, r *http.Request) response.Response {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.DevLXDErrorResponse(err)
	}

	if !requestor.IsTrusted() {
		return response.DevLXDErrorResponse(api.NewGenericStatusError(http.StatusForbidden))
	}

	return response.EmptySyncResponse
}

// allowDevLXDPermission returns a wrapper that checks access to a given LXD entity
// (e.g. image, instance, network).
//
// The mux route variables required to identify the entity must be passed in.
// For example, an instance needs its name, so the mux var "name" should be provided.
// Always pass mux vars in the same order they appear in the API route.
func allowDevLXDPermission(entityType entity.Type, entitlement auth.Entitlement, muxVars ...string) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		var err error
		var entityURL *api.URL

		s := d.State()

		// Disallow cross-project access.
		err = enforceDevLXDProject(r)
		if err != nil {
			return response.DevLXDErrorResponse(err)
		}

		inst, err := request.GetContextValue[instance.Instance](r.Context(), request.CtxDevLXDInstance)
		if err != nil {
			return response.DevLXDErrorResponse(err)
		}

		instProject := inst.Project().Name

		if entityType == entity.TypeProject && len(muxVars) == 0 {
			entityURL = entity.ProjectURL(instProject)
		} else {
			muxValues := make([]string, 0, len(muxVars))
			vars := mux.Vars(r)
			for _, muxVar := range muxVars {
				muxValue := vars[muxVar]
				if muxValue == "" {
					return response.DevLXDErrorResponse(fmt.Errorf("Failed to perform permission check: Path argument label %q not found in request URL %q", muxVar, r.URL))
				}

				muxValues = append(muxValues, muxValue)
			}

			targetParam := request.QueryParam(r, "target")

			entityURL, err = entityType.URL(instProject, targetParam, muxValues...)
			if err != nil {
				return response.DevLXDErrorResponse(fmt.Errorf("Failed to perform permission check: %w", err))
			}
		}

		// Validate whether the user has the needed permission.
		err = s.Authorizer.CheckPermission(r.Context(), entityURL, entitlement)
		if err != nil {
			return response.DevLXDErrorResponse(err)
		}

		return response.EmptySyncResponse
	}
}

// getInstanceFromContextAndCheckSecurityFlags retrieves the instance from the provided request
// context and verifies that the instance has the provided devLXD security features enabled.
func getInstanceFromContextAndCheckSecurityFlags(ctx context.Context, keys ...DevLXDSecurityKey) (instance.Instance, error) {
	inst, err := request.GetContextValue[instance.Instance](ctx, request.CtxDevLXDInstance)
	if err != nil {
		return nil, err
	}

	if !hasInstanceSecurityFeatures(inst.ExpandedConfig(), keys...) {
		return nil, api.NewGenericStatusError(http.StatusForbidden)
	}

	return inst, nil
}

// hasInstanceSecurityFeatures checks whether the instance has the provided devLXD security features enabled.
func hasInstanceSecurityFeatures(expandedConfig map[string]string, keys ...DevLXDSecurityKey) bool {
	for _, key := range keys {
		value := expandedConfig[string(key)]

		// The devLXD is enabled by default, therefore we only prevent access if the feature
		// is explicitly disabled (set to "false"). All other features must be explicitly enabled.
		if shared.IsFalse(value) || (value == "" && key != devLXDSecurityKey) {
			return false
		}
	}

	return true
}
