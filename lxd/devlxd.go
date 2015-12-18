package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

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
	f func(c container, r *http.Request) *devLxdResponse
}

var configGet = devLxdHandler{"/1.0/config", func(c container, r *http.Request) *devLxdResponse {
	filtered := []string{}
	for k, _ := range c.ExpandedConfig() {
		if strings.HasPrefix(k, "user.") {
			filtered = append(filtered, fmt.Sprintf("/1.0/config/%s", k))
		}
	}
	return okResponse(filtered, "json")
}}

var configKeyGet = devLxdHandler{"/1.0/config/{key}", func(c container, r *http.Request) *devLxdResponse {
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

var metadataGet = devLxdHandler{"/1.0/meta-data", func(c container, r *http.Request) *devLxdResponse {
	value := c.ExpandedConfig()["user.meta-data"]
	return okResponse(fmt.Sprintf("#cloud-config\ninstance-id: %s\nlocal-hostname: %s\n%s", c.Name(), c.Name(), value), "raw")
}}

var handlers = []devLxdHandler{
	devLxdHandler{"/", func(c container, r *http.Request) *devLxdResponse {
		return okResponse([]string{"/1.0"}, "json")
	}},
	devLxdHandler{"/1.0", func(c container, r *http.Request) *devLxdResponse {
		return okResponse(shared.Jmap{"api_compat": 0}, "json")
	}},
	configGet,
	configKeyGet,
	metadataGet,
	/* TODO: events */
}

func hoistReq(f func(container, *http.Request) *devLxdResponse, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn := extractUnderlyingConn(w)
		pid, ok := pidMapper.m[conn]
		if !ok {
			http.Error(w, pidNotInContainerErr.Error(), 500)
			return
		}

		c, err := findContainerForPid(pid, d)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		resp := f(c, r)
		if resp.code != http.StatusOK {
			w.Header().Set("Content-Type", "application/octet-stream")
			http.Error(w, fmt.Sprintf("%s", resp.content), resp.code)
		} else if resp.ctype == "json" {
			w.Header().Set("Content-Type", "application/json")
			WriteJSON(w, resp.content)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprintf(w, resp.content.(string))
		}
	}
}

func createAndBindDevLxd() (*net.UnixListener, error) {
	sockFile := path.Join(shared.VarPath("devlxd"), "sock")

	/*
	 * If this socket exists, that means a previous lxd died and didn't
	 * clean up after itself. We assume that the LXD is actually dead if we
	 * get this far, since StartDaemon() tries to connect to the actual lxd
	 * socket to make sure that it is actually dead. So, it is safe to
	 * remove it here without any checks.
	 *
	 * Also, it would be nice to SO_REUSEADDR here so we don't have to
	 * delete the socket, but we can't:
	 *   http://stackoverflow.com/questions/15716302/so-reuseaddr-and-af-unix
	 *
	 * Note that this will force clients to reconnect when LXD is restarted.
	 */
	if err := os.Remove(sockFile); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	unixAddr, err := net.ResolveUnixAddr("unix", sockFile)
	if err != nil {
		return nil, err
	}

	unixl, err := net.ListenUnix("unix", unixAddr)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(sockFile, 0666); err != nil {
		return nil, err
	}

	return unixl, nil
}

func devLxdServer(d *Daemon) *http.Server {
	m := mux.NewRouter()

	for _, handler := range handlers {
		m.HandleFunc(handler.path, hoistReq(handler.f, d))
	}

	return &http.Server{
		Handler:   m,
		ConnState: pidMapper.ConnStateHandler,
	}
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
var pidMapper = ConnPidMapper{m: map[*net.UnixConn]int32{}}

type ConnPidMapper struct {
	m map[*net.UnixConn]int32
}

func (m *ConnPidMapper) ConnStateHandler(conn net.Conn, state http.ConnState) {
	unixConn := conn.(*net.UnixConn)
	switch state {
	case http.StateNew:
		pid, err := getPid(unixConn)
		if err != nil {
			shared.Debugf("Error getting pid for conn %s", err)
		} else {
			m.m[unixConn] = pid
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
		delete(m.m, unixConn)
	case http.StateClosed:
		delete(m.m, unixConn)
	default:
		shared.Debugf("Unknown state for connection %s", state)
	}
}

/*
 * I also don't see that golang exports an API to get at the underlying FD, but
 * we need it to get at SO_PEERCRED, so let's grab it.
 */
func extractUnderlyingFd(unixConnPtr *net.UnixConn) int {
	conn := reflect.Indirect(reflect.ValueOf(unixConnPtr))
	netFdPtr := conn.FieldByName("fd")
	netFd := reflect.Indirect(netFdPtr)
	fd := netFd.FieldByName("sysfd")
	return int(fd.Int())
}

func getPid(conn *net.UnixConn) (int32, error) {
	fd := extractUnderlyingFd(conn)

	_, _, pid, err := getUcred(fd)
	if err != nil {
		return 0, err
	}

	return pid, nil
}

/*
 * As near as I can tell, there is no nice way of extracting an underlying
 * net.Conn (or in our case, net.UnixConn) from an http.Request or
 * ResponseWriter without hijacking it [1]. Since we want to send and recieve
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
	 *    so the process isn't actually a decendant of the container's
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
			name := parts[len(parts)-1]

			return containerLoadByName(d, name)
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

	containers, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return nil, err
	}

	for _, container := range containers {
		c, err := containerLoadByName(d, container)
		if err != nil {
			return nil, err
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
