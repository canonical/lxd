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
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// devLXDServer creates an http.Server capable of handling requests against the
// /dev/lxd Unix socket endpoint created inside containers.
func devLXDServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler:     devLXDAPI(d, containerAuthenticator{}),
		ConnState:   pidMapper.ConnStateHandler,
		ConnContext: request.SaveConnectionInContext,
	}
}

// containerAuthenticator implements DevLXDAuthenticator for Unix socket connections.
type containerAuthenticator struct{}

// IsVsock returns false indicating that this authenticator is not used for vsock connections.
func (containerAuthenticator) IsVsock() bool {
	return false
}

// hoistReqContainer identifies the calling container based on the Unix socket credentials,
// verifies it's the container's root user, and returns the container instance.
func (containerAuthenticator) AuthenticateInstance(d *Daemon, r *http.Request) (instance.Instance, error) {
	conn := ucred.GetConnFromContext(r.Context())

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, api.NewStatusError(http.StatusInternalServerError, "Not a unix connection")
	}

	cred := pidMapper.GetConnUcred(unixConn)
	if cred == nil {
		return nil, api.NewStatusError(http.StatusInternalServerError, errPIDNotInContainer.Error())
	}

	s := d.State()

	c, err := devlxdFindContainerForPID(s, cred.Pid)
	if err != nil {
		return nil, api.NewStatusError(http.StatusInternalServerError, err.Error())
	}

	// Access control
	rootUID := uint32(0)

	idmapset, err := c.CurrentIdmap()
	if err == nil && idmapset != nil {
		uid, _ := idmapset.ShiftIntoNs(0, 0)
		rootUID = uint32(uid)
	}

	if rootUID != cred.Uid {
		return nil, api.NewStatusError(http.StatusUnauthorized, "Access denied for non-root user")
	}

	return c, nil
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

// getLXCMonitorContainer returns the container instance for the candidateMonitorPID, but only if:
// 1. The process is a LXC monitor process (identified by the command line starting with "[lxc monitor]").
// 2. The process does not have a PID in another PID namespace (checked via the NSpid in /proc/<pid>/status).
// 3. The process name contains the container's name (extracted from /proc/<pid>/cmdline).
// If the process is not a LXC monitor process or has a PID in another PID namespace, it returns nil and the parent PID.
func getLXCMonitorContainer(s *state.State, candidateMonitorPID int32) (c instance.Container, parentPID int32, err error) {
	candidateMonitorPIDStr := strconv.FormatInt(int64(candidateMonitorPID), 10)

	// Extract the parent process ID and whether the NSpid matches the PID.
	{
		statusBytes, err := os.ReadFile("/proc/" + candidateMonitorPIDStr + "/status")
		if err != nil {
			return nil, -1, fmt.Errorf("Failed to read status for PID %d: %w", candidateMonitorPID, err)
		}

		// Parse status file to find the parent PID and check if the NSpid matches the PID.
		nsPIDMatchesPID := false
		for line := range strings.SplitSeq(string(statusBytes), "\n") {
			ppidStr, found := strings.CutPrefix(line, "PPid:")
			if found {
				// ParseUint to avoid scanning for `-` sign.
				ppid, err := strconv.ParseUint(strings.TrimSpace(ppidStr), 10, 32)
				if err != nil {
					return nil, -1, fmt.Errorf("Failed to parse parent PID from status for PID %d: %w", candidateMonitorPID, err)
				}

				if ppid > math.MaxInt32 {
					return nil, -1, fmt.Errorf("PPid value too large for PID %d: Upper bound exceeded", candidateMonitorPID)
				}

				parentPID = int32(ppid)
			}

			nspidStr, found := strings.CutPrefix(line, "NSpid:")
			if found {
				nsPIDMatchesPID = strings.TrimSpace(nspidStr) == candidateMonitorPIDStr
			}
		}

		if !nsPIDMatchesPID {
			// Process is not monitor as has a PID in another PID namespace.
			return nil, parentPID, nil
		}
	}

	// Read command line of the monitor process, extract the container name and load the container instance.
	{
		cmdLineBytes, err := os.ReadFile("/proc/" + candidateMonitorPIDStr + "/cmdline")
		if err != nil {
			return nil, -1, fmt.Errorf("Failed to read command line for PID %d: %w", candidateMonitorPID, err)
		}

		cmdLine := strings.TrimSuffix(string(cmdLineBytes), "\x00")

		// Check if command line starts with "[lxc monitor]".
		if !strings.HasPrefix(cmdLine, "[lxc monitor]") {
			return nil, parentPID, nil
		}

		// Extract the container name from the command line.
		parts := strings.Split(cmdLine, " ") // Container names can't have spaces.
		name := parts[len(parts)-1]

		projectName := api.ProjectDefaultName
		if strings.Contains(name, "_") {
			projectName, name, _ = strings.Cut(name, "_")
		}

		// Load the container instance by project and name.
		inst, err := instance.LoadByProjectAndName(s, projectName, name)
		if err != nil {
			return nil, -1, fmt.Errorf("Failed to load instance %q in project %q: %w", name, projectName, err)
		}

		if inst.Type() != instancetype.Container {
			return nil, -1, api.StatusErrorf(http.StatusNotFound, "Instance %q in project %q is not a container", inst.Name(), inst.Project().Name)
		}

		c, ok := inst.(instance.Container)
		if !ok {
			return nil, -1, fmt.Errorf("Invalid container type %T for %q in project %q", inst, inst.Name(), inst.Project().Name)
		}

		return c, parentPID, nil // Successfully loaded the container instance.
	}
}

var errPIDNotInContainer = errors.New("Process ID not found in container")

// devlxdFindContainerForPID finds the container for a given devlxd origin process ID (originPID).
// The originPID does not need to be the LXC monitor process, but it should be in the same PID namespace as the
// container (this is enforced by the function).
func devlxdFindContainerForPID(s *state.State, originPID int32) (instance.Container, error) {
	/*
	 * Try and figure out which container a pid is in. There is probably a
	 * better way to do this. Based on rharper's initial performance
	 * metrics, looping over every container and calling newLxdContainer is
	 * expensive, so I wanted to avoid that if possible, so this happens in
	 * a two step process:
	 *
	 * 1. Walk up the process tree until you see something that looks like
	 *    an lxc monitor process and extract its name from there.
	 *    This approach is used when a process is started within the container.
	 *
	 * 2. If this fails, it may be that someone did an `lxc exec foo -- bash`,
	 *    so the process isn't actually a descendant of the container's
	 *    init. In this case we just look through all the containers until
	 *    we find an init with a matching pid namespace. This is probably
	 *    uncommon, so hopefully the slowness won't hurt us.
	 */
	pid := originPID
	for pid > 1 {
		c, ppid, err := getLXCMonitorContainer(s, pid)
		if err != nil {
			logger.Warn("Failed matching PID to container for devlxd", logger.Ctx{"pid": pid, "err": err})
			return nil, errPIDNotInContainer // Don't return error to avoid leaking details about the process.
		}

		if c != nil {
			return c, nil // Matched to a container, stop the search.
		}

		// Continue walking up the process tree looking for LXC monitor processes.
		pid = ppid
	}

	// Get origin PID namespace.
	// This is used to check if the origin is in the same PID namespace as the container we are trying to find.
	originPIDNamespace, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", originPID))
	if err != nil {
		logger.Warn("Failed to read devlxd origin PID namespace", logger.Ctx{"pid": originPID, "err": err})

		// Don't return error to avoid leaking details about the process.
		return nil, errPIDNotInContainer
	}

	// Fallback to loading all containers and checking their PID namespaces.
	// This is the approach used when the process isn't actually a descendant of the container's init process,
	// such as when using `lxc exec`.
	instances, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		logger.Warn("Failed loading instances for devlxd", logger.Ctx{"pid": originPID, "err": err})

		// Don't return error to avoid leaking details about the process.
		return nil, errPIDNotInContainer
	}

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
