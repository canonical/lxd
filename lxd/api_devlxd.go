package main

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// devLXDServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside containers.
func devLXDServer(d *Daemon) *http.Server {
	isVsock := false

	return &http.Server{
		Handler:     devLXDAPI(d, hoistReqContainer, isVsock),
		ConnState:   pidMapper.ConnStateHandler,
		ConnContext: request.SaveConnectionInContext,
	}
}

// hoistReqContainer identifies the calling container based on the Unix socket credentials,
// verifies it's the container's root user, and passes the identified container to the handler.
func hoistReqContainer(d *Daemon, r *http.Request, handler devLXDAPIHandlerFunc) response.Response {
	conn := ucred.GetConnFromContext(r.Context())

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusInternalServerError, "Not a unix connection"))
	}

	cred := pidMapper.GetConnUcred(unixConn)
	if cred == nil {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusInternalServerError, errPIDNotInContainer.Error()))
	}

	s := d.State()

	c, err := devlxdFindContainerForPID(s, cred.Pid)
	if err != nil {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusInternalServerError, err.Error()))
	}

	// Access control
	rootUID := uint32(0)

	idmapset, err := c.CurrentIdmap()
	if err == nil && idmapset != nil {
		uid, _ := idmapset.ShiftIntoNs(0, 0)
		rootUID = uint32(uid)
	}

	if rootUID != cred.Uid {
		return response.DevLXDErrorResponse(api.NewStatusError(http.StatusUnauthorized, "Access denied for non-root user"))
	}

	request.SetContextValue(r, request.CtxDevLXDInstance, c)
	return handler(d, r)
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

// loadContainerFromLXCMonitorPID loads a container instance based on the name of its LXC monitor process PID.
// This function trusts that the lxcMonitorPID is the PID of the LXC monitor process for the container.
// It does not check the PID namespace, so it should only be used when the caller is sure that the PID is correct.
func loadContainerFromLXCMonitorPID(s *state.State, lxcMonitorPID int32) (instance.Container, error) {
	procPID := "/proc/" + strconv.Itoa(int(lxcMonitorPID))
	cmdLine, err := os.ReadFile(procPID + "/cmdline")
	if err != nil {
		return nil, err
	}

	// Container names can't have spaces.
	parts := strings.Split(string(cmdLine), " ")
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
		return nil, api.StatusErrorf(http.StatusNotFound, "Instance %q in project %q is not a container", inst.Name(), inst.Project().Name)
	}

	c, ok := inst.(instance.Container)
	if !ok {
		return nil, fmt.Errorf("Invalid container type %T for %q in project %q", inst, inst.Name(), inst.Project().Name)
	}

	return c, nil
}

var errPIDNotInContainer = errors.New("Process ID not found in container")

// devlxdFindContainerForPID finds the container for a given devlxd origin process ID (originPID).
// The originPID does not need to be the LXC monitor process, but it should be in the same PID namespace as the
// container (this is enforced by the function).
func devlxdFindContainerForPID(s *state.State, originPID int32) (instance.Container, error) {
	// Check if the container's PID namespace matches the originPIDNamespace.
	// Doesn't return an error to avoid leaking details about the process.
	checkPIDNamespace := func(c instance.Container, originPIDNamespace string) bool {
		initPID := c.InitPID()
		if initPID <= 0 {
			return false
		}

		pidNs, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", initPID))
		if err != nil {
			logger.Warn("Failed to read process init PID namespace", logger.Ctx{"project": c.Project().Name, "instance": c.Name(), "pid": initPID, "err": err})

			return false
		}

		if originPIDNamespace != pidNs {
			return false
		}

		return true
	}

	// Record origin PID and its PID namespace.
	// This is used to check if the origin is in the same PID namespace as the container we are trying to find.
	originPIDNamespace, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", originPID))
	if err != nil {
		logger.Warn("Failed to read devlxd origin PID namespace", logger.Ctx{"pid": originPID, "err": err})

		// Don't return error to avoid leaking details about the process.
		return nil, errPIDNotInContainer
	}

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
	pid := originPID
	for pid > 1 {
		c, err := loadContainerFromLXCMonitorPID(s, pid)
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			logger.Warn("Failed matching PID to container for devlxd", logger.Ctx{"pid": pid, "err": err})
			return nil, errPIDNotInContainer // Don't return error to avoid leaking details about the process.
		} else if c != nil {
			// Found a candidate LXC monitor process and container from the pid, but we need to check
			// if it is in the same PID namespace as the originPIDNamespace to ensure we have the right
			// container.
			if checkPIDNamespace(c, originPIDNamespace) {
				return c, nil // Matched to a container in the same PID namespace, stop the search.
			}
		}

		// Continue walking up the process tree looking for LXC monitor processes.
		procPID := "/proc/" + strconv.Itoa(int(pid))
		status, err := os.ReadFile(procPID + "/status")
		if err != nil {
			return nil, err
		}

		for line := range strings.SplitSeq(string(status), "\n") {
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
				return nil, errors.New("PPid value too large: Upper bound exceeded")
			}

			pid = int32(ppid)
			break
		}
	}

	// Fallback to loading all containers and checking their PID namespaces.
	instances, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		logger.Warn("Failed loading instances for devlxd", logger.Ctx{"pid": originPID, "err": err})

		// Don't return error to avoid leaking details about the process.
		return nil, errPIDNotInContainer
	}

	for _, inst := range instances {
		if inst.Type() != instancetype.Container {
			continue
		}

		c, ok := inst.(instance.Container)
		if !ok {
			continue
		}

		if !c.IsRunning() {
			continue
		}

		ok = checkPIDNamespace(c, originPIDNamespace)
		if !ok {
			continue
		}

		return c, nil
	}

	return nil, errPIDNotInContainer
}
