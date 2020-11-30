package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/gorilla/mux"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/daemon"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/ucred"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// DevLxdServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside containers.
func devLxdServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler:   devLxdAPI(d),
		ConnState: pidMapper.ConnStateHandler,
	}
}

type devLxdResponse struct {
	content interface{}
	code    int
	ctype   string
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
	f func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse
}

var devlxdConfigGet = devLxdHandler{"/1.0/config", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	filtered := []string{}
	for k := range c.ExpandedConfig() {
		if strings.HasPrefix(k, "user.") {
			filtered = append(filtered, fmt.Sprintf("/1.0/config/%s", k))
		}
	}
	return okResponse(filtered, "json")
}}

var devlxdConfigKeyGet = devLxdHandler{"/1.0/config/{key}", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	key := mux.Vars(r)["key"]
	if !strings.HasPrefix(key, "user.") {
		return &devLxdResponse{"not authorized", http.StatusForbidden, "raw"}
	}

	value, ok := c.ExpandedConfig()[key]
	if !ok {
		return &devLxdResponse{"not found", http.StatusNotFound, "raw"}
	}

	return okResponse(value, "raw")
}}

var devlxdImageExport = devLxdHandler{"/1.0/images/{fingerprint}/export", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	if !shared.IsTrue(c.ExpandedConfig()["security.devlxd.images"]) {
		return &devLxdResponse{"not authorized", http.StatusForbidden, "raw"}
	}

	// Use by security checks to distinguish devlxd vs lxd APIs
	r.RemoteAddr = "@devlxd"

	resp := imageExport(d, r)
	err := resp.Render(w)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	return &devLxdResponse{"", http.StatusOK, "raw"}
}}

var devlxdMetadataGet = devLxdHandler{"/1.0/meta-data", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	value := c.ExpandedConfig()["user.meta-data"]
	return okResponse(fmt.Sprintf("#cloud-config\ninstance-id: %s\nlocal-hostname: %s\n%s", c.Name(), c.Name(), value), "raw")
}}

var devlxdEventsGet = devLxdHandler{"/1.0/events", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "config,device"
	}

	conn, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}
	defer conn.Close() // This ensures the go routine below is ended when this function ends.

	listener, err := d.devlxdEvents.AddListener(strconv.Itoa(c.ID()), conn, strings.Split(typeStr, ","), "", false)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	logger.Debugf("New container event listener for '%s': %s", project.Instance(c.Project(), c.Name()), listener.ID())

	// Create a cancellable context from the request context. Once the request has been upgraded
	// to a websocket the request's context doesn't appear to be cancelled when the client
	// disconnects (even though its documented as such). But we wrap the request's context here
	// anyway just in case its fixed in the future.
	ctx, cancel := context.WithCancel(r.Context())

	// Instead of relying on the request's context to be cancelled when the client connection
	// is closed (see above), we instead enter into a repeat read loop of the connection in
	// order to detect when the client connection is closed. This should be fine as for the
	// events route there is no expectation to read any useful data from the client.
	go func() {
		for {
			_, _, err := conn.NextReader()
			if err != nil {
				// Client read error (likely premature close), so cancel context.
				cancel()
				return
			}
		}
	}()

	listener.Wait(ctx)

	return &devLxdResponse{"websocket", http.StatusOK, "websocket"}
}}

var handlers = []devLxdHandler{
	{"/", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse {
		return okResponse([]string{"/1.0"}, "json")
	}},
	{"/1.0", func(d *Daemon, c instance.Instance, w http.ResponseWriter, r *http.Request) *devLxdResponse {
		return okResponse(shared.Jmap{"api_version": version.APIVersion}, "json")
	}},
	devlxdConfigGet,
	devlxdConfigKeyGet,
	devlxdMetadataGet,
	devlxdEventsGet,
	devlxdImageExport,
}

func hoistReq(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) *devLxdResponse, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn := extractUnderlyingConn(w)
		cred, ok := pidMapper.m[conn]
		if !ok {
			http.Error(w, pidNotInContainerErr.Error(), 500)
			return
		}

		c, err := findContainerForPid(cred.Pid, d.State())
		if err != nil {
			http.Error(w, err.Error(), 500)
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
			http.Error(w, "Access denied for non-root user", 401)
			return
		}

		resp := f(d, c, w, r)
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

type ConnPidMapper struct {
	m     map[*net.UnixConn]*unix.Ucred
	mLock sync.Mutex
}

func (m *ConnPidMapper) ConnStateHandler(conn net.Conn, state http.ConnState) {
	unixConn := conn.(*net.UnixConn)
	switch state {
	case http.StateNew:
		cred, err := ucred.GetCred(unixConn)
		if err != nil {
			logger.Debugf("Error getting ucred for conn %s", err)
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
		logger.Debugf("Unknown state for connection %s", state)
	}
}

/*
 * As near as I can tell, there is no nice way of extracting an underlying
 * net.Conn (or in our case, net.UnixConn) from an http.Request or
 * ResponseWriter without hijacking it [1]. Since we want to send and receive
 * unix creds to figure out which container this request came from, we need to
 * do this.
 *
 * [1]: https://groups.google.com/forum/#!topic/golang-nuts/_FWdFXJa6QA
 */
func extractUnderlyingConn(w http.ResponseWriter) *net.UnixConn {
	v := reflect.Indirect(reflect.ValueOf(w))
	connPtr := v.FieldByName("conn")
	conn := reflect.Indirect(connPtr)
	rwc := conn.FieldByName("rwc")

	netConnPtr := (*net.Conn)(unsafe.Pointer(rwc.UnsafeAddr()))
	unixConnPtr := (*netConnPtr).(*net.UnixConn)

	return unixConnPtr
}

var pidNotInContainerErr = fmt.Errorf("pid not in container?")

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
	 * 2. If this fails, it may be that someone did an `lxc exec foo bash`,
	 *    so the process isn't actually a descendant of the container's
	 *    init. In this case we just look through all the containers until
	 *    we find an init with a matching pid namespace. This is probably
	 *    uncommon, so hopefully the slowness won't hurt us.
	 */

	origpid := pid

	for pid > 1 {
		cmdline, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return nil, err
		}

		if strings.HasPrefix(string(cmdline), "[lxc monitor]") {
			// container names can't have spaces
			parts := strings.Split(string(cmdline), " ")
			name := strings.TrimSuffix(parts[len(parts)-1], "\x00")

			projectName := project.Default
			if strings.Contains(name, "_") {
				fields := strings.SplitN(name, "_", 2)
				projectName = fields[0]
				name = fields[1]
			}

			inst, err := instance.LoadByProjectAndName(s, projectName, name)
			if err != nil {
				return nil, err
			}

			if inst.Type() != instancetype.Container {
				return nil, fmt.Errorf("Instance is not container type")
			}

			return inst.(instance.Container), nil
		}

		status, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			return nil, err
		}

		re := regexp.MustCompile("PPid:\\s*([0-9]*)")
		for _, line := range strings.Split(string(status), "\n") {
			m := re.FindStringSubmatch(line)
			if m != nil && len(m) > 1 {
				result, err := strconv.Atoi(m[1])
				if err != nil {
					return nil, err
				}

				pid = int32(result)
				break
			}
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
			return inst.(instance.Container), nil
		}
	}

	return nil, pidNotInContainerErr
}
