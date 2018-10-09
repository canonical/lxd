package main

import (
	"encoding/json"
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
	"time"
	"unsafe"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// DevLxdServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside containers.
func DevLxdServer(d *Daemon) *http.Server {
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
	f func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse
}

var devlxdConfigGet = devLxdHandler{"/1.0/config", func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	filtered := []string{}
	for k := range c.ExpandedConfig() {
		if strings.HasPrefix(k, "user.") {
			filtered = append(filtered, fmt.Sprintf("/1.0/config/%s", k))
		}
	}
	return okResponse(filtered, "json")
}}

var devlxdConfigKeyGet = devLxdHandler{"/1.0/config/{key}", func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse {
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

var devlxdImageExport = devLxdHandler{"/1.0/images/{fingerprint}/export", func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse {
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

var devlxdMetadataGet = devLxdHandler{"/1.0/meta-data", func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	value := c.ExpandedConfig()["user.meta-data"]
	return okResponse(fmt.Sprintf("#cloud-config\ninstance-id: %s\nlocal-hostname: %s\n%s", c.Name(), c.Name(), value), "raw")
}}

var devlxdEventsLock sync.Mutex
var devlxdEventListeners map[int]map[string]*eventListener = make(map[int]map[string]*eventListener)

var devlxdEventsGet = devLxdHandler{"/1.0/events", func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse {
	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "config,device"
	}

	conn, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return &devLxdResponse{"internal server error", http.StatusInternalServerError, "raw"}
	}

	listener := eventListener{
		project:      c.Project(),
		active:       make(chan bool, 1),
		connection:   conn,
		id:           uuid.NewRandom().String(),
		messageTypes: strings.Split(typeStr, ","),
	}

	devlxdEventsLock.Lock()
	cid := c.Id()
	_, ok := devlxdEventListeners[cid]
	if !ok {
		devlxdEventListeners[cid] = map[string]*eventListener{}
	}
	devlxdEventListeners[cid][listener.id] = &listener
	devlxdEventsLock.Unlock()

	logger.Debugf("New container event listener for '%s': %s", c.Name(), listener.id)

	<-listener.active

	return &devLxdResponse{"websocket", http.StatusOK, "websocket"}
}}

func devlxdEventSend(c container, eventType string, eventMessage interface{}) error {
	event := shared.Jmap{}
	event["type"] = eventType
	event["timestamp"] = time.Now()
	event["metadata"] = eventMessage

	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	devlxdEventsLock.Lock()
	cid := c.Id()
	listeners, ok := devlxdEventListeners[cid]
	if !ok {
		devlxdEventsLock.Unlock()
		return nil
	}

	for _, listener := range listeners {
		if !shared.StringInSlice(eventType, listener.messageTypes) {
			continue
		}

		go func(listener *eventListener, body []byte) {
			// Check that the listener still exists
			if listener == nil {
				return
			}

			// Ensure there is only a single even going out at the time
			listener.lock.Lock()
			defer listener.lock.Unlock()

			// Make sure we're not done already
			if listener.done {
				return
			}

			err = listener.connection.WriteMessage(websocket.TextMessage, body)
			if err != nil {
				// Remove the listener from the list
				devlxdEventsLock.Lock()
				delete(devlxdEventListeners[cid], listener.id)
				devlxdEventsLock.Unlock()

				// Disconnect the listener
				listener.connection.Close()
				listener.active <- false
				listener.done = true
				logger.Debugf("Disconnected container event listener for '%s': %s", c.Name(), listener.id)
			}
		}(listener, body)
	}
	devlxdEventsLock.Unlock()

	return nil
}

var handlers = []devLxdHandler{
	{"/", func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse {
		return okResponse([]string{"/1.0"}, "json")
	}},
	{"/1.0", func(d *Daemon, c container, w http.ResponseWriter, r *http.Request) *devLxdResponse {
		return okResponse(shared.Jmap{"api_version": version.APIVersion}, "json")
	}},
	devlxdConfigGet,
	devlxdConfigKeyGet,
	devlxdMetadataGet,
	devlxdEventsGet,
	devlxdImageExport,
}

func hoistReq(f func(*Daemon, container, http.ResponseWriter, *http.Request) *devLxdResponse, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn := extractUnderlyingConn(w)
		cred, ok := pidMapper.m[conn]
		if !ok {
			http.Error(w, pidNotInContainerErr.Error(), 500)
			return
		}

		c, err := findContainerForPid(cred.pid, d)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Access control
		rootUid := int64(0)

		idmapset, err := c.LastIdmapSet()
		if err == nil && idmapset != nil {
			uid, _ := idmapset.ShiftIntoNs(0, 0)
			rootUid = int64(uid)
		}

		if rootUid != cred.uid {
			http.Error(w, "Access denied for non-root user", 401)
			return
		}

		resp := f(d, c, w, r)
		if resp.code != http.StatusOK {
			http.Error(w, fmt.Sprintf("%s", resp.content), resp.code)
		} else if resp.ctype == "json" {
			w.Header().Set("Content-Type", "application/json")
			util.WriteJSON(w, resp.content, debug)
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
var pidMapper = ConnPidMapper{m: map[*net.UnixConn]*ucred{}}

type ucred struct {
	pid int32
	uid int64
	gid int64
}

type ConnPidMapper struct {
	m     map[*net.UnixConn]*ucred
	mLock sync.Mutex
}

func (m *ConnPidMapper) ConnStateHandler(conn net.Conn, state http.ConnState) {
	unixConn := conn.(*net.UnixConn)
	switch state {
	case http.StateNew:
		cred, err := getCred(unixConn)
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
 * I also don't see that golang exports an API to get at the underlying FD, but
 * we need it to get at SO_PEERCRED, so let's grab it.
 */
func extractUnderlyingFd(unixConnPtr *net.UnixConn) (int, error) {
	conn := reflect.Indirect(reflect.ValueOf(unixConnPtr))

	netFdPtr := conn.FieldByName("fd")
	if !netFdPtr.IsValid() {
		return -1, fmt.Errorf("Unable to extract fd from net.UnixConn")
	}
	netFd := reflect.Indirect(netFdPtr)

	fd := netFd.FieldByName("sysfd")
	if !fd.IsValid() {
		// Try under the new name
		pfdPtr := netFd.FieldByName("pfd")
		if !pfdPtr.IsValid() {
			return -1, fmt.Errorf("Unable to extract pfd from netFD")
		}
		pfd := reflect.Indirect(pfdPtr)

		fd = pfd.FieldByName("Sysfd")
		if !fd.IsValid() {
			return -1, fmt.Errorf("Unable to extract Sysfd from poll.FD")
		}
	}

	return int(fd.Int()), nil
}

func getCred(conn *net.UnixConn) (*ucred, error) {
	fd, err := extractUnderlyingFd(conn)
	if err != nil {
		return nil, err
	}

	uid, gid, pid, err := getUcred(fd)
	if err != nil {
		return nil, err
	}

	return &ucred{pid, int64(uid), int64(gid)}, nil
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

func findContainerForPid(pid int32, d *Daemon) (container, error) {
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

			project := "default"
			if strings.Contains(name, "_") {
				project = strings.Split(name, "_")[0]
			}

			return containerLoadByProjectAndName(d.State(), project, name)
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

	containers, err := containerLoadNodeAll(d.State())
	if err != nil {
		return nil, err
	}

	for _, c := range containers {
		if !c.IsRunning() {
			continue
		}

		initpid := c.InitPID()
		pidNs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", initpid))
		if err != nil {
			return nil, err
		}

		if origPidNs == pidNs {
			return c, nil
		}
	}

	return nil, pidNotInContainerErr
}
