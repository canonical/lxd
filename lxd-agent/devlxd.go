package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

type devLXDHandlerFunc func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse

// devLXDServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside VMs.
func devLXDServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler: devLXDAPI(d),
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

func getVsockClient(d *Daemon) (lxd.InstanceServer, error) {
	// Try connecting to LXD server.
	client, err := getClient(d.serverCID, int(d.serverPort), d.serverCertificate)
	if err != nil {
		return nil, err
	}

	server, err := lxd.ConnectLXDHTTP(nil, client)
	if err != nil {
		return nil, err
	}

	return server, nil
}

var devLXDConfigGet = devLXDHandler{
	path:        "/1.0/config",
	handlerFunc: devLXDConfigGetHandler,
}

func devLXDConfigGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	client, err := getVsockClient(d)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	defer client.Disconnect()

	resp, _, err := client.RawQuery("GET", "/1.0/config", nil, "")
	if err != nil {
		return smartResponse(err)
	}

	var config []string

	err = resp.MetadataAsStruct(&config)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed parsing response from LXD: %w", err))
	}

	filtered := []string{}
	for _, k := range config {
		if strings.HasPrefix(k, "/1.0/config/user.") || strings.HasPrefix(k, "/1.0/config/cloud-init.") {
			filtered = append(filtered, k)
		}
	}
	return okResponse(filtered, "json")
}

var devLXDConfigKeyGet = devLXDHandler{
	path:        "/1.0/config/{key}",
	handlerFunc: devLXDConfigKeyGetHandler,
}

func devLXDConfigKeyGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	key, err := url.PathUnescape(mux.Vars(r)["key"])
	if err != nil {
		return &devLXDResponse{"bad request", http.StatusBadRequest, "raw"}
	}

	if !strings.HasPrefix(key, "user.") && !strings.HasPrefix(key, "cloud-init.") {
		return &devLXDResponse{"not authorized", http.StatusForbidden, "raw"}
	}

	client, err := getVsockClient(d)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	defer client.Disconnect()

	resp, _, err := client.RawQuery("GET", "/1.0/config/"+key, nil, "")
	if err != nil {
		return smartResponse(err)
	}

	var value string

	err = resp.MetadataAsStruct(&value)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed parsing response from LXD: %w", err))
	}

	return okResponse(value, "raw")
}

var devLXDMetadataGet = devLXDHandler{
	path:        "/1.0/meta-data",
	handlerFunc: devLXDMetadataGetHandler,
}

func devLXDMetadataGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	var client lxd.InstanceServer
	var err error

	for i := 0; i < 10; i++ {
		client, err = getVsockClient(d)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	defer client.Disconnect()

	resp, _, err := client.RawQuery("GET", "/1.0/meta-data", nil, "")
	if err != nil {
		return smartResponse(err)
	}

	var metaData string

	err = resp.MetadataAsStruct(&metaData)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed parsing response from LXD: %w", err))
	}

	return okResponse(metaData, "raw")
}

var devLXDEventsGet = devLXDHandler{
	path:        "/1.0/events",
	handlerFunc: devLXDEventsGetHandler,
}

func devLXDEventsGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	err := eventsGet(d, r).Render(w, r)
	if err != nil {
		return smartResponse(err)
	}

	return okResponse("", "raw")
}

var devLXDAPIGet = devLXDHandler{
	path:        "/1.0",
	handlerFunc: devLXDAPIGetHandler,
}

func devLXDAPIGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	client, err := getVsockClient(d)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	defer client.Disconnect()

	if r.Method == "GET" {
		resp, _, err := client.RawQuery(r.Method, "/1.0", nil, "")
		if err != nil {
			return smartResponse(err)
		}

		var instanceData api.DevLXDGet

		err = resp.MetadataAsStruct(&instanceData)
		if err != nil {
			return smartResponse(fmt.Errorf("Failed parsing response from LXD: %w", err))
		}

		return okResponse(instanceData, "json")
	} else if r.Method == "PATCH" {
		_, _, err := client.RawQuery(r.Method, "/1.0", r.Body, "")
		if err != nil {
			return smartResponse(err)
		}

		return okResponse("", "raw")
	}

	return &devLXDResponse{`method "` + r.Method + `" not allowed`, http.StatusBadRequest, "raw"}
}

var devLXDDevicesGet = devLXDHandler{
	path:        "/1.0/devices",
	handlerFunc: devLXDDevicesGetHandler,
}

func devLXDDevicesGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	client, err := getVsockClient(d)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	defer client.Disconnect()

	resp, _, err := client.RawQuery("GET", "/1.0/devices", nil, "")
	if err != nil {
		return smartResponse(err)
	}

	var devices config.Devices

	err = resp.MetadataAsStruct(&devices)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed parsing response from LXD: %w", err))
	}

	return okResponse(devices, "json")
}

var devLXDImageExport = devLXDHandler{
	path:        "/1.0/images/{fingerprint}/export",
	handlerFunc: devLXDImageExportHandler,
}

func devLXDImageExportHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	// Extract the fingerprint.
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return smartResponse(err)
	}

	// Get a http.Client.
	client, err := getClient(d.serverCID, int(d.serverPort), d.serverCertificate)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	// Remove the request URI, this cannot be set on requests.
	r.RequestURI = ""

	// Set up the request URL with the correct host.
	r.URL = &api.NewURL().Scheme("https").Host("custom.socket").Path(version.APIVersion, "images", fingerprint, "export").URL

	// Proxy the request.
	resp, err := client.Do(r)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, err.Error())
	}

	// Set headers from the host LXD.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Set(k, v)
		}
	}

	// Copy headers and response body.
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return smartResponse(err)
	}

	return nil
}

var devLXDUbuntuProGet = devLXDHandler{
	path:        "/1.0/ubuntu-pro",
	handlerFunc: devLXDUbuntuProGetHandler,
}

func devLXDUbuntuProGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	if r.Method != http.MethodGet {
		return errorResponse(http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}

	// Get a http.Client.
	client, err := getClient(d.serverCID, int(d.serverPort), d.serverCertificate)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	// Remove the request URI, this cannot be set on requests.
	r.RequestURI = ""

	// Set up the request URL with the correct host.
	r.URL = &api.NewURL().Scheme("https").Host("custom.socket").Path(version.APIVersion, "ubuntu-pro").URL

	// Proxy the request.
	resp, err := client.Do(r)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, err.Error())
	}

	var apiResponse api.Response
	err = json.NewDecoder(resp.Body).Decode(&apiResponse)
	if err != nil {
		return smartResponse(err)
	}

	var settingsResponse api.UbuntuProSettings
	err = json.Unmarshal(apiResponse.Metadata, &settingsResponse)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, fmt.Sprintf("Invalid Ubuntu Token settings response received from host: %v", err))
	}

	return okResponse(settingsResponse, "json")
}

var devLXDUbuntuProTokenPost = devLXDHandler{
	path:        "/1.0/ubuntu-pro/token",
	handlerFunc: devLXDUbuntuProTokenPostHandler,
}

func devLXDUbuntuProTokenPostHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
	if r.Method != http.MethodPost {
		return errorResponse(http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
	}

	// Get a http.Client.
	client, err := getClient(d.serverCID, int(d.serverPort), d.serverCertificate)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	// Remove the request URI, this cannot be set on requests.
	r.RequestURI = ""

	// Set up the request URL with the correct host.
	r.URL = &api.NewURL().Scheme("https").Host("custom.socket").Path(version.APIVersion, "ubuntu-pro", "token").URL

	// Proxy the request.
	resp, err := client.Do(r)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, err.Error())
	}

	var apiResponse api.Response
	err = json.NewDecoder(resp.Body).Decode(&apiResponse)
	if err != nil {
		return smartResponse(err)
	}

	if apiResponse.StatusCode != http.StatusOK {
		return errorResponse(apiResponse.Code, apiResponse.Error)
	}

	var tokenResponse api.UbuntuProGuestTokenResponse
	err = json.Unmarshal(apiResponse.Metadata, &tokenResponse)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, fmt.Sprintf("Invalid Ubuntu Token response received from host: %v", err))
	}

	return okResponse(tokenResponse, "json")
}

var handlers = []devLXDHandler{
	{
		path: "/",
		handlerFunc: func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLXDResponse {
			return okResponse([]string{"/1.0"}, "json")
		},
	},
	devLXDAPIGet,
	devLXDConfigGet,
	devLXDConfigKeyGet,
	devLXDMetadataGet,
	devLXDEventsGet,
	devLXDDevicesGet,
	devLXDImageExport,
	devLXDUbuntuProGet,
	devLXDUbuntuProTokenPost,
}

func hoistReq(f func(*Daemon, http.ResponseWriter, *http.Request) *devLXDResponse, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := f(d, w, r)
		if resp == nil {
			// The handler has already written the response.
			return
		}

		if resp.code != http.StatusOK {
			http.Error(w, fmt.Sprint(resp.content), resp.code)
		} else if resp.ctype == "json" {
			w.Header().Set("Content-Type", "application/json")

			_ = util.WriteJSON(w, resp.content, nil)
		} else if resp.ctype != "websocket" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = fmt.Fprint(w, resp.content.(string))
		}
	}
}

func devLXDAPI(d *Daemon) http.Handler {
	m := mux.NewRouter()
	m.UseEncodedPath() // Allow encoded values in path segments.

	for _, handler := range handlers {
		m.HandleFunc(handler.path, hoistReq(handler.handlerFunc, d))
	}

	return m
}

// Create a new net.Listener bound to the unix socket of the devLXD endpoint.
func createDevLXDListener(dir string) (net.Listener, error) {
	path := filepath.Join(dir, "lxd", "sock")

	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return nil, err
	}

	// If this socket exists, that means a previous LXD instance died and
	// didn't clean up. We assume that such LXD instance is actually dead
	// if we get this far, since localCreateListener() tries to connect to
	// the actual lxd socket to make sure that it is actually dead. So, it
	// is safe to remove it here without any checks.
	//
	// Also, it would be nice to SO_REUSEADDR here so we don't have to
	// delete the socket, but we can't:
	//   http://stackoverflow.com/questions/15716302/so-reuseaddr-and-af-unix
	//
	// Note that this will force clients to reconnect when LXD is restarted.
	err = socketUnixRemoveStale(path)
	if err != nil {
		return nil, err
	}

	listener, err := socketUnixListen(path)
	if err != nil {
		return nil, err
	}

	err = socketUnixSetPermissions(path, 0600)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	return listener, nil
}

// Remove any stale socket file at the given path.
func socketUnixRemoveStale(path string) error {
	// If there's no socket file at all, there's nothing to do.
	if !shared.PathExists(path) {
		return nil
	}

	logger.Debugf("Detected stale unix socket, deleting")
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("could not delete stale local socket: %w", err)
	}

	return nil
}

// Change the file mode of the given unix socket file.
func socketUnixSetPermissions(path string, mode os.FileMode) error {
	err := os.Chmod(path, mode)
	if err != nil {
		return fmt.Errorf("cannot set permissions on local socket: %w", err)
	}

	return nil
}

// Bind to the given unix socket path.
func socketUnixListen(path string) (net.Listener, error) {
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve socket address: %w", err)
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind socket: %w", err)
	}

	return listener, err
}
