package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
	"github.com/grant-he/lxd/lxd/daemon"
	"github.com/grant-he/lxd/lxd/util"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/logger"
	"github.com/grant-he/lxd/shared/version"
)

// DevLxdServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside VMs.
func devLxdServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler: devLxdAPI(d),
	}
}

type devLxdResponse struct {
	content interface{}
	code    int
	ctype   string
}

type instanceData struct {
	Name   string            `json:"name"`
	Config map[string]string `json:"config,omitempty"`
}

func okResponse(ct interface{}, ctype string) *devLxdResponse {
	return &devLxdResponse{ct, http.StatusOK, ctype}
}

type devLxdHandler struct {
	path string

	/*
	 * This API will have to be changed slightly when we decide to support
	 * websocket events upgrading, but since we don't have events on the
	 * server side right now either, I went the simple route to avoid
	 * needless noise.
	 */
	f func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse
}

var devlxdConfigGet = devLxdHandler{"/1.0/config", func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	data, err := ioutil.ReadFile("instance-data")
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	var instance instanceData

	err = json.Unmarshal(data, &instance)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	filtered := []string{}
	for k := range instance.Config {
		if strings.HasPrefix(k, "user.") {
			filtered = append(filtered, fmt.Sprintf("/1.0/config/%s", k))
		}
	}
	return okResponse(filtered, "json")
}}

var devlxdConfigKeyGet = devLxdHandler{"/1.0/config/{key}", func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	key := mux.Vars(r)["key"]
	if !strings.HasPrefix(key, "user.") {
		return &devLxdResponse{"not authorized", http.StatusForbidden, "raw"}
	}

	data, err := ioutil.ReadFile("instance-data")
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	var instance instanceData

	err = json.Unmarshal(data, &instance)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	value, ok := instance.Config[key]
	if !ok {
		return &devLxdResponse{"not found", http.StatusNotFound, "raw"}
	}

	return okResponse(value, "raw")
}}

var devlxdMetadataGet = devLxdHandler{"/1.0/meta-data", func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	data, err := ioutil.ReadFile("instance-data")
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	var instance instanceData

	err = json.Unmarshal(data, &instance)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	value := instance.Config["user.meta-data"]
	return okResponse(fmt.Sprintf("#cloud-config\ninstance-id: %s\nlocal-hostname: %s\n%s", instance.Name, instance.Name, value), "raw")
}}

var devLxdEventsGet = devLxdHandler{"/1.0/events", func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	err := eventsGet(d, r).Render(w)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	return okResponse("", "raw")
}}

var handlers = []devLxdHandler{
	{"/", func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
		return okResponse([]string{"/1.0"}, "json")
	}},
	{"/1.0", func(d *Daemon, w http.ResponseWriter, r *http.Request) *devLxdResponse {
		return okResponse(shared.Jmap{"api_version": version.APIVersion}, "json")
	}},
	devlxdConfigGet,
	devlxdConfigKeyGet,
	devlxdMetadataGet,
	devLxdEventsGet,
}

func hoistReq(f func(*Daemon, http.ResponseWriter, *http.Request) *devLxdResponse, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := f(d, w, r)
		if resp.code != http.StatusOK {
			http.Error(w, fmt.Sprintf("%s", resp.content), resp.code)
		} else if resp.ctype == "json" {
			w.Header().Set("Content-Type", "application/json")
			util.WriteJSON(w, resp.content, daemon.Debug)
		} else if resp.ctype != "websocket" {
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprintf(w, resp.content.(string))
		}
	}
}

func devLxdAPI(d *Daemon) http.Handler {
	m := mux.NewRouter()

	for _, handler := range handlers {
		m.HandleFunc(handler.path, hoistReq(handler.f, d))
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
		listener.Close()
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
		return fmt.Errorf("could not delete stale local socket: %v", err)
	}

	return nil
}

// Change the file mode of the given unix socket file,
func socketUnixSetPermissions(path string, mode os.FileMode) error {
	err := os.Chmod(path, mode)
	if err != nil {
		return fmt.Errorf("cannot set permissions on local socket: %v", err)
	}
	return nil
}

// Bind to the given unix socket path.
func socketUnixListen(path string) (net.Listener, error) {
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve socket address: %v", err)
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind socket: %v", err)
	}

	return listener, err

}
