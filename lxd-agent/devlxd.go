package main

import (
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

type devLXDHandlerFunc func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse

// DevLxdServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside VMs.
func devLxdServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler: devLxdAPI(d),
	}
}

type devLxdHandler struct {
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

var devlxdConfigGet = devLxdHandler{
	path:        "/1.0/config",
	handlerFunc: devlxdConfigGetHandler,
}

func devlxdConfigGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
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

var devlxdConfigKeyGet = devLxdHandler{
	path:        "/1.0/config/{key}",
	handlerFunc: devlxdConfigKeyGetHandler,
}

func devlxdConfigKeyGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	key, err := url.PathUnescape(mux.Vars(r)["key"])
	if err != nil {
		return &devLxdResponse{"bad request", http.StatusBadRequest, "raw"}
	}

	if !strings.HasPrefix(key, "user.") && !strings.HasPrefix(key, "cloud-init.") {
		return &devLxdResponse{"not authorized", http.StatusForbidden, "raw"}
	}

	client, err := getVsockClient(d)
	if err != nil {
		return smartResponse(fmt.Errorf("Failed connecting to LXD over vsock: %w", err))
	}

	defer client.Disconnect()

	resp, _, err := client.RawQuery("GET", fmt.Sprintf("/1.0/config/%s", key), nil, "")
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

var devlxdMetadataGet = devLxdHandler{
	path:        "/1.0/meta-data",
	handlerFunc: devlxdMetadataGetHandler,
}

func devlxdMetadataGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
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

var devLxdEventsGet = devLxdHandler{
	path:        "/1.0/events",
	handlerFunc: devlxdEventsGetHandler,
}

func devlxdEventsGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	err := eventsGet(d, r).Render(w, r)
	if err != nil {
		return smartResponse(err)
	}

	return okResponse("", "raw")
}

var devlxdAPIGet = devLxdHandler{
	path:        "/1.0",
	handlerFunc: devlxdAPIGetHandler,
}

func devlxdAPIGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
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

	return &devLxdResponse{fmt.Sprintf("method %q not allowed", r.Method), http.StatusBadRequest, "raw"}
}

var devlxdDevicesGet = devLxdHandler{
	path:        "/1.0/devices",
	handlerFunc: devlxdDevicesGetHandler,
}

func devlxdDevicesGetHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
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

var devlxdImageExport = devLxdHandler{
	path:        "/1.0/images/{fingerprint}/export",
	handlerFunc: devlxdImageExportHandler,
}

func devlxdImageExportHandler(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
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

var handlers = []devLxdHandler{
	{
		path: "/",
		handlerFunc: func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
			return okResponse([]string{"/1.0"}, "json")
		},
	},
	devlxdAPIGet,
	devlxdConfigGet,
	devlxdConfigKeyGet,
	devlxdMetadataGet,
	devLxdEventsGet,
	devlxdDevicesGet,
	devlxdImageExport,
}

func hoistReq(f func(*Daemon, http.ResponseWriter, *http.Request) *devLxdResponse, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := f(d, w, r)
		if resp == nil {
			// The handler has already written the response.
			return
		}

		if resp.code != http.StatusOK {
			http.Error(w, fmt.Sprintf("%s", resp.content), resp.code)
		} else if resp.ctype == "json" {
			w.Header().Set("Content-Type", "application/json")

			_ = util.WriteJSON(w, resp.content, nil)
		} else if resp.ctype != "websocket" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = fmt.Fprint(w, resp.content.(string))
		}
	}
}

func devLxdAPI(d *Daemon) http.Handler {
	m := mux.NewRouter()
	m.UseEncodedPath() // Allow encoded values in path segments.

	for _, handler := range handlers {
		m.HandleFunc(handler.path, hoistReq(handler.handlerFunc, d))
	}

	return m
}

// Create a new net.Listener bound to the unix socket of the devlxd endpoint.
func createDevLxdlListener(dir string) (net.Listener, error) {
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
