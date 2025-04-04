package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cloudinit"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
	"github.com/canonical/lxd/shared/ws"
)

type hoistFunc func(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) response.Response, d *Daemon) func(http.ResponseWriter, *http.Request)

type devLXDHandlerFunc func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response

// devLXDServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside containers.
func devLXDServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler:     devLXDAPI(d, hoistReq),
		ConnState:   pidMapper.ConnStateHandler,
		ConnContext: request.SaveConnectionInContext,
	}
}

type devLXDHandler struct {
	path string

	/*
	 * This API will have to be changed slightly when we decide to support
	 * websocket events upgrading, but since we don't have events on the
	 * server side right now either, I went the simple route to avoid
	 * needless noise.
	 */
	handlerFunc devLXDHandlerFunc
}

var devLXDConfigGet = devLXDHandler{
	path:        "/1.0/config",
	handlerFunc: devLXDConfigGetHandler,
}

func devLXDConfigGetHandler(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
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

	return response.DevLxdResponse(http.StatusOK, filtered, "json", c.Type() == instancetype.VM)
}

var devLXDConfigKeyGet = devLXDHandler{
	path:        "/1.0/config/{key}",
	handlerFunc: devLXDConfigKeyGetHandler,
}

func devLXDConfigKeyGetHandler(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	key, err := url.PathUnescape(mux.Vars(r)["key"])
	if err != nil {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusBadRequest, "bad request"), c.Type() == instancetype.VM)
	}

	if !strings.HasPrefix(key, "user.") && !strings.HasPrefix(key, "cloud-init.") {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
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
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusNotFound, "not found"), c.Type() == instancetype.VM)
	}

	return response.DevLxdResponse(http.StatusOK, value, "raw", c.Type() == instancetype.VM)
}

var devLXDImageExport = devLXDHandler{
	path:        "/1.0/images/{fingerprint}/export",
	handlerFunc: devLXDImageExportHandler,
}

func devLXDImageExportHandler(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	if shared.IsFalseOrEmpty(c.ExpandedConfig()["security.devlxd.images"]) {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	return imageExport(d, r)
}

var devLXDMetadataGet = devLXDHandler{
	path:        "/1.0/meta-data",
	handlerFunc: devLXDMetadataGetHandler,
}

func devLXDMetadataGetHandler(d *Daemon, inst instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(inst.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), inst.Type() == instancetype.VM)
	}

	value := inst.ExpandedConfig()["user.meta-data"]

	return response.DevLxdResponse(http.StatusOK, "instance-id: "+inst.CloudInitID()+"\nlocal-hostname: "+inst.Name()+"\n"+value, "raw", inst.Type() == instancetype.VM)
}

var devLXDEventsGet = devLXDHandler{
	path:        "/1.0/events",
	handlerFunc: devLXDEventsGetHandler,
}

func devLXDEventsGetHandler(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
	}

	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "config,device"
	}

	var listenerConnection events.EventListenerConnection
	var resp response.Response

	// If the client has not requested a websocket connection then fallback to long polling event stream mode.
	if r.Header.Get("Upgrade") == "websocket" {
		conn, err := ws.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection = events.NewWebsocketListenerConnection(conn)

		resp = response.DevLxdResponse(http.StatusOK, "websocket", "websocket", c.Type() == instancetype.VM)
	} else {
		h, ok := w.(http.Hijacker)
		if !ok {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		conn, _, err := h.Hijack()
		if err != nil {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection, err = events.NewStreamListenerConnection(conn)
		if err != nil {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
		}

		resp = response.DevLxdResponse(http.StatusOK, "", "raw", c.Type() == instancetype.VM)
	}

	listener, err := d.State().DevlxdEvents.AddListener(c.ID(), listenerConnection, strings.Split(typeStr, ","))
	if err != nil {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
	}

	logger.Debug("New container event listener", logger.Ctx{"instance": c.Name(), "project": c.Project().Name, "listener_id": listener.ID})
	listener.Wait(r.Context())

	return resp
}

var devLXDAPIHandler = devLXDHandler{
	path:        "/1.0",
	handlerFunc: devLXDAPIHandlerFunc,
}

func devLXDAPIHandlerFunc(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	s := d.State()

	if r.Method == "GET" {
		var location string
		if d.serverClustered {
			location = c.Location()
		} else {
			var err error

			location, err = os.Hostname()
			if err != nil {
				return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "internal server error"), c.Type() == instancetype.VM)
			}
		}

		var state api.StatusCode

		if shared.IsTrue(c.LocalConfig()["volatile.last_state.ready"]) {
			state = api.Ready
		} else {
			state = api.Started
		}

		return response.DevLxdResponse(http.StatusOK, api.DevLXDGet{APIVersion: version.APIVersion, Location: location, InstanceType: c.Type().String(), DevLXDPut: api.DevLXDPut{State: state.String()}}, "json", c.Type() == instancetype.VM)
	} else if r.Method == "PATCH" {
		if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
		}

		req := api.DevLXDPut{}

		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid request body: %w", err), c.Type() == instancetype.VM)
		}

		state := api.StatusCodeFromString(req.State)

		if state != api.Started && state != api.Ready {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusBadRequest, "Invalid state %q", req.State), c.Type() == instancetype.VM)
		}

		err = c.VolatileSet(map[string]string{"volatile.last_state.ready": strconv.FormatBool(state == api.Ready)})
		if err != nil {
			return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusInternalServerError, "Failed to set instance state: %w", err), c.Type() == instancetype.VM)
		}

		if state == api.Ready {
			s.Events.SendLifecycle(c.Project().Name, lifecycle.InstanceReady.Event(c, nil))
		}

		return response.DevLxdResponse(http.StatusOK, "", "raw", c.Type() == instancetype.VM)
	}

	return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusMethodNotAllowed, "method %q not allowed", r.Method), c.Type() == instancetype.VM)
}

var devLXDDevicesGet = devLXDHandler{
	path:        "/1.0/devices",
	handlerFunc: devLXDDevicesGetHandler,
}

func devLXDDevicesGetHandler(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.StatusErrorf(http.StatusForbidden, "not authorized"), c.Type() == instancetype.VM)
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

	return response.DevLxdResponse(http.StatusOK, c.ExpandedDevices(), "json", c.Type() == instancetype.VM)
}

var devLXDUbuntuProGet = devLXDHandler{
	path:        "/1.0/ubuntu-pro",
	handlerFunc: devLXDUbuntuProGetHandler,
}

func devLXDUbuntuProGetHandler(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.NewGenericStatusError(http.StatusForbidden), c.Type() == instancetype.VM)
	}

	if r.Method != http.MethodGet {
		return response.DevLxdErrorResponse(api.NewGenericStatusError(http.StatusMethodNotAllowed), c.Type() == instancetype.VM)
	}

	settings := d.State().UbuntuPro.GuestAttachSettings(c.ExpandedConfig()["ubuntu_pro.guest_attach"])

	// Otherwise, return the value from the instance configuration.
	return response.DevLxdResponse(http.StatusOK, settings, "json", c.Type() == instancetype.VM)
}

var devLXDUbuntuProTokenPost = devLXDHandler{
	path:        "/1.0/ubuntu-pro/token",
	handlerFunc: devLXDUbuntuProTokenPostHandler,
}

func devLXDUbuntuProTokenPostHandler(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
	if shared.IsFalse(c.ExpandedConfig()["security.devlxd"]) {
		return response.DevLxdErrorResponse(api.NewGenericStatusError(http.StatusForbidden), c.Type() == instancetype.VM)
	}

	if r.Method != http.MethodPost {
		return response.DevLxdErrorResponse(api.NewGenericStatusError(http.StatusMethodNotAllowed), c.Type() == instancetype.VM)
	}

	// Return http.StatusForbidden if the host does not have guest attachment enabled.
	tokenJSON, err := d.State().UbuntuPro.GetGuestToken(r.Context(), c.ExpandedConfig()["ubuntu_pro.guest_attach"])
	if err != nil {
		return response.DevLxdErrorResponse(fmt.Errorf("Failed to get an Ubuntu Pro guest token: %w", err), c.Type() == instancetype.VM)
	}

	// Pass it back to the guest.
	return response.DevLxdResponse(http.StatusOK, tokenJSON, "json", c.Type() == instancetype.VM)
}

var handlers = []devLXDHandler{
	{
		path: "/",
		handlerFunc: func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) response.Response {
			return response.DevLxdResponse(http.StatusOK, []string{"/1.0"}, "json", c.Type() == instancetype.VM)
		},
	},
	devLXDAPIHandler,
	devLXDConfigGet,
	devLXDConfigKeyGet,
	devLXDMetadataGet,
	devLXDEventsGet,
	devLXDImageExport,
	devLXDDevicesGet,
	devLXDUbuntuProGet,
	devLXDUbuntuProTokenPost,
}

func hoistReq(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) response.Response, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set devLXD auth method to identify this request as coming from the /dev/lxd socket.
		request.SetCtxValue(r, request.CtxProtocol, auth.AuthenticationMethodDevLXD)

		conn := ucred.GetConnFromContext(r.Context())

		cred := pidMapper.GetConnUcred(conn.(*net.UnixConn))
		if cred == nil {
			http.Error(w, errPIDNotInContainer.Error(), http.StatusInternalServerError)
			return
		}

		s := d.State()

		c, err := findContainerForPid(cred.Pid, s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Access control
		rootUID := uint32(0)

		idmapset, err := c.CurrentIdmap()
		if err == nil && idmapset != nil {
			uid, _ := idmapset.ShiftIntoNs(0, 0)
			rootUID = uint32(uid)
		}

		if rootUID != cred.Uid {
			http.Error(w, "Access denied for non-root user", http.StatusUnauthorized)
			return
		}

		resp := f(d, c, w, r)
		if resp != nil {
			err = resp.Render(w, r)
			if err != nil {
				writeErr := response.DevLxdErrorResponse(err, false).Render(w, r)
				if writeErr != nil {
					logger.Warn("Failed writing error for HTTP response", logger.Ctx{"url": r.URL, "err": err, "writeErr": writeErr})
				}
			}
		}
	}
}

func devLXDAPI(d *Daemon, f hoistFunc) http.Handler {
	m := mux.NewRouter()
	m.UseEncodedPath() // Allow encoded values in path segments.

	for _, handler := range handlers {
		m.HandleFunc(handler.path, f(handler.handlerFunc, d))
	}

	return m
}

/*
 * Everything below here is the guts of the unix socket bits. Unfortunately,
 * golang's API does not make this easy. What happens is:
 *
 * 1. We install a ConnState listener on the http.Server, which does the
 *    initial unix socket credential exchange. When we get a connection started
 *    event, we use SO_PEERCRED to extract the creds for the socket.
 *
 * 2. We store a map from the connection pointer to the pid for that
 *    connection, so that once the HTTP negotiation occurrs and we get a
 *    ResponseWriter, we know (because we negotiated on the first byte) which
 *    pid the connection belogs to.
 *
 * 3. Regular HTTP negotiation and dispatch occurs via net/http.
 *
 * 4. When rendering the response via ResponseWriter, we match its underlying
 *    connection against what we stored in step (2) to figure out which container
 *    it came from.
 */

/*
 * We keep this in a global so that we can reference it from the server and
 * from our http handlers, since there appears to be no way to pass information
 * around here.
 */
var pidMapper = ConnPidMapper{m: map[*net.UnixConn]*unix.Ucred{}}

// ConnPidMapper is threadsafe cache of unix connections to process IDs. We use this in hoistReq to determine
// the instance that the connection has been made from.
type ConnPidMapper struct {
	m     map[*net.UnixConn]*unix.Ucred
	mLock sync.Mutex
}

// ConnStateHandler is used in the `ConnState` field of the devLXD http.Server so that we can cache the process ID of the
// caller when a new connection is made and delete it when the connection is closed.
func (m *ConnPidMapper) ConnStateHandler(conn net.Conn, state http.ConnState) {
	unixConn, _ := conn.(*net.UnixConn)
	if unixConn == nil {
		logger.Error("Invalid type for devlxd connection", logger.Ctx{"conn_type": fmt.Sprintf("%T", conn)})
		return
	}

	switch state {
	case http.StateNew:
		cred, err := ucred.GetCred(unixConn)
		if err != nil {
			logger.Debug("Error getting ucred for devlxd connection", logger.Ctx{"err": err})
		} else {
			m.mLock.Lock()
			m.m[unixConn] = cred
			m.mLock.Unlock()
		}

	case http.StateActive:
		return
	case http.StateIdle:
		return
	case http.StateHijacked:
		/*
		 * The "Hijacked" state indicates that the connection has been
		 * taken over from net/http. This is useful for things like
		 * developing websocket libraries, who want to upgrade the
		 * connection to a websocket one, and not use net/http any
		 * more. Whatever the case, we want to forget about it since we
		 * won't see it either.
		 */
		m.mLock.Lock()
		delete(m.m, unixConn)
		m.mLock.Unlock()
	case http.StateClosed:
		m.mLock.Lock()
		delete(m.m, unixConn)
		m.mLock.Unlock()
	default:
		logger.Debug("Unknown state for devlxd connection", logger.Ctx{"state": state.String()})
	}
}

// GetConnUcred returns a previously stored ucred associated to a connection.
// Returns nil if no ucred found for the connection.
func (m *ConnPidMapper) GetConnUcred(conn *net.UnixConn) *unix.Ucred {
	m.mLock.Lock()
	defer m.mLock.Unlock()
	return pidMapper.m[conn]
}

var errPIDNotInContainer = errors.New("Process ID not found in container")

func findContainerForPid(pid int32, s *state.State) (instance.Container, error) {
	/*
	 * Try and figure out which container a pid is in. There is probably a
	 * better way to do this. Based on rharper's initial performance
	 * metrics, looping over every container and calling newLxdContainer is
	 * expensive, so I wanted to avoid that if possible, so this happens in
	 * a two step process:
	 *
	 * 1. Walk up the process tree until you see something that looks like
	 *    an lxc monitor process and extract its name from there.
	 *
	 * 2. If this fails, it may be that someone did an `lxc exec foo -- bash`,
	 *    so the process isn't actually a descendant of the container's
	 *    init. In this case we just look through all the containers until
	 *    we find an init with a matching pid namespace. This is probably
	 *    uncommon, so hopefully the slowness won't hurt us.
	 */

	origpid := pid

	for pid > 1 {
		procPID := "/proc/" + fmt.Sprint(pid)
		cmdline, err := os.ReadFile(procPID + "/cmdline")
		if err != nil {
			return nil, err
		}

		if strings.HasPrefix(string(cmdline), "[lxc monitor]") {
			// container names can't have spaces
			parts := strings.Split(string(cmdline), " ")
			name := strings.TrimSuffix(parts[len(parts)-1], "\x00")

			projectName := api.ProjectDefaultName
			if strings.Contains(name, "_") {
				projectName, name, _ = strings.Cut(name, "_")
			}

			inst, err := instance.LoadByProjectAndName(s, projectName, name)
			if err != nil {
				return nil, err
			}

			if inst.Type() != instancetype.Container {
				return nil, fmt.Errorf("Instance is not container type")
			}

			// Explicitly ignore type assertion check. We've just checked that it's a container.
			c, _ := inst.(instance.Container)
			return c, nil
		}

		status, err := os.ReadFile(procPID + "/status")
		if err != nil {
			return nil, err
		}

		for _, line := range strings.Split(string(status), "\n") {
			ppidStr, found := strings.CutPrefix(line, "PPid:")
			if !found {
				continue
			}

			// ParseUint avoid scanning for `-` sign.
			ppid, err := strconv.ParseUint(strings.TrimSpace(ppidStr), 10, 32)
			if err != nil {
				return nil, err
			}

			if ppid > math.MaxInt32 {
				return nil, fmt.Errorf("PPid value too large: Upper bound exceeded")
			}

			pid = int32(ppid)
			break
		}
	}

	origPidNs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", origpid))
	if err != nil {
		return nil, err
	}

	instances, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		return nil, err
	}

	for _, inst := range instances {
		if inst.Type() != instancetype.Container {
			continue
		}

		if !inst.IsRunning() {
			continue
		}

		initpid := inst.InitPID()
		pidNs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", initpid))
		if err != nil {
			return nil, err
		}

		if origPidNs == pidNs {
			// Explicitly ignore type assertion check. The instance must be a container if we've found it via the process ID.
			c, _ := inst.(instance.Container)
			return c, nil
		}
	}

	return nil, errPIDNotInContainer
}
