package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/gorilla/mux"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

func socketPath() string {
	return shared.VarPath("devlxd")
}

type DevLxdResponse struct {
	content interface{}
	code    int
}

func OkResponse(ct interface{}) *DevLxdResponse {
	return &DevLxdResponse{ct, http.StatusOK}
}

type DevLxdHandler struct {
	path string

	/*
	 * This API will have to be changed slightly when we decide to support
	 * websocket events upgrading, but since we don't have events on the
	 * server side right now either, I went the simple route to avoid
	 * needless noise.
	 */
	f func(c *lxdContainer, r *http.Request) *DevLxdResponse
}

var configGet = DevLxdHandler{"/1.0/config", func(c *lxdContainer, r *http.Request) *DevLxdResponse {
	return OkResponse(c.config)
}}

var configKeyGet = DevLxdHandler{"/1.0/config/{key}", func(c *lxdContainer, r *http.Request) *DevLxdResponse {
	key := mux.Vars(r)["key"]
	value, ok := c.config[key]
	if !ok {
		return &DevLxdResponse{"not found", http.StatusNotFound}
	}

	return OkResponse(value)
}}

var metadataGet = DevLxdHandler{"/1.0/meta-data", func(c *lxdContainer, r *http.Request) *DevLxdResponse {
	return &DevLxdResponse{"not implemented", http.StatusNotImplemented}
}}

var handlers = []DevLxdHandler{
	DevLxdHandler{"/", func(c *lxdContainer, r *http.Request) *DevLxdResponse {
		return OkResponse([]string{"/1.0"})
	}},
	DevLxdHandler{"/1.0", func(c *lxdContainer, r *http.Request) *DevLxdResponse {
		return OkResponse(shared.Jmap{"api_compat": 0})
	}},
	configGet,
	configKeyGet,
	metadataGet,
	/* TODO: events */
}

func hoistReq(f func(*lxdContainer, *http.Request) *DevLxdResponse, d *Daemon) func(http.ResponseWriter, *http.Request) {
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
			http.Error(w, fmt.Sprintf("%s", resp.content), resp.code)
		} else {
			WriteJson(w, resp.content)
		}
	}
}

func createAndBindDevLxd() (*net.UnixListener, error) {
	path := socketPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("couldn't remove old devlxd: %s", err)
	}

	unixAddr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, err
	}

	unixl, err := net.ListenUnix("unix", unixAddr)
	if err != nil {
		return nil, err
	}

	if err := enablePassingCreds(unixl); err != nil {
		return nil, err
	}

	if err := os.Chmod(path, 0666); err != nil {
		return nil, err
	}

	return unixl, nil
}

func setupDevLxdMount(c *lxc.Container) error {
	mtab := fmt.Sprintf("%s dev/lxd none bind,create=file 0 0", socketPath())
	return c.SetConfigItem("lxc.mount.entry", mtab)
}

func devLxdServer(d *Daemon) http.Server {
	m := mux.NewRouter()

	for _, handler := range handlers {
		m.HandleFunc(handler.path, hoistReq(handler.f, d))
	}

	return http.Server{
		Handler:   m,
		ConnState: pidMapper.ConnStateHandler,
	}
}

/*
 * Everything below here is the guts of the unix socket bits. Unfortunately,
 * golang's API does not make this easy. What happens is:
 *
 * 1. We install a ConnState listener on the http.Server, which does the
 *    initial unix socet credential exchange. golang writes a byte before any http
 *    stuff can happen [1], so clients must do this too (hence us exporting
 *    lxd.DevLxdTransport, so at least golang clients don't have to fuck around
 *    with this themselves).
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
 *
 * [1]: https://github.com/golang/go/issues/6476
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
			shared.Debugf("error getting pid for conn %s", err)
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
		shared.Debugf("unknown state for connection %s", state)
	}
}

/*
 * This is unfortunate. The underlying fd for the connection is not exported,
 * so we have to resort to hacks. If we don't set SO_PASSCRED on at least one
 * side of the socket, we can't pass creds. Since it would be silly to require
 * every client to set this option, let's just do it ourselves.
 */
func enablePassingCreds(connPtr *net.UnixListener) error {
	conn := reflect.Indirect(reflect.ValueOf(connPtr))
	netFdPtr := conn.FieldByName("fd")
	netFd := reflect.Indirect(netFdPtr)
	fd := netFd.FieldByName("sysfd")
	return syscall.SetsockoptInt(int(fd.Int()), syscall.SOL_SOCKET, syscall.SO_PASSCRED, 1)
}

func getPid(conn *net.UnixConn) (int32, error) {

	oob := make([]byte, 1024)

	regn, oobn, flags, _, err := conn.ReadMsgUnix(nil, oob)
	if err != nil {
		return -1, err
	}

	if flags != 0 {
		return -1, fmt.Errorf("flags not zero? 0x%x", flags)
	}

	if oobn == 0 {
		return -1, fmt.Errorf("didn't get creds, did you set SO_PASSCRED? %d regular bytes", regn)
	}

	oob = oob[:oobn]

	scm, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return -1, err
	}

	creds, err := syscall.ParseUnixCredentials(&scm[0])
	if err != nil {
		return -1, err
	}

	return creds.Pid, nil
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

func findContainerForPid(pid int32, d *Daemon) (*lxdContainer, error) {

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

			return newLxdContainer(name, d)
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

	containers, err := dbListContainers(d)
	if err != nil {
		return nil, err
	}

	for _, container := range containers {
		c, err := newLxdContainer(container, d)
		if err != nil {
			return nil, err
		}

		pidNs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", c.c.InitPid()))
		if err != nil {
			return nil, err
		}

		if origPidNs == pidNs {
			return c, nil
		}
	}

	return nil, pidNotInContainerErr
}
