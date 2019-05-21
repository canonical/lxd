package bindings

/*
#include <stdlib.h>
#include <unistd.h>
#include <string.h>
#include <errno.h>
#include <fcntl.h>

#include <dqlite.h>
#include <sqlite3.h>

int dup_cloexec(int oldfd) {
	int newfd = -1;

	newfd = dup(oldfd);
	if (newfd < 0) {
		return -1;
	}

	if (fcntl(newfd, F_SETFD, FD_CLOEXEC) < 0) {
		return -1;
	}

	return newfd;
}
*/
import "C"

import (
	"fmt"
	"net"
	"os"
	"unsafe"

	"github.com/pkg/errors"
)

// ProtocolVersion is the latest dqlite server protocol version.
const ProtocolVersion = uint64(C.DQLITE_PROTOCOL_VERSION)

// Request types.
const (
	RequestLeader    = C.DQLITE_REQUEST_LEADER
	RequestClient    = C.DQLITE_REQUEST_CLIENT
	RequestHeartbeat = C.DQLITE_REQUEST_HEARTBEAT
	RequestOpen      = C.DQLITE_REQUEST_OPEN
	RequestPrepare   = C.DQLITE_REQUEST_PREPARE
	RequestExec      = C.DQLITE_REQUEST_EXEC
	RequestQuery     = C.DQLITE_REQUEST_QUERY
	RequestFinalize  = C.DQLITE_REQUEST_FINALIZE
	RequestExecSQL   = C.DQLITE_REQUEST_EXEC_SQL
	RequestQuerySQL  = C.DQLITE_REQUEST_QUERY_SQL
	RequestInterrupt = C.DQLITE_REQUEST_INTERRUPT
)

// Response types.
const (
	ResponseFailure = C.DQLITE_RESPONSE_FAILURE
	ResponseServer  = C.DQLITE_RESPONSE_SERVER
	ResponseWelcome = C.DQLITE_RESPONSE_WELCOME
	ResponseServers = C.DQLITE_RESPONSE_SERVERS
	ResponseDb      = C.DQLITE_RESPONSE_DB
	ResponseStmt    = C.DQLITE_RESPONSE_STMT
	ResponseResult  = C.DQLITE_RESPONSE_RESULT
	ResponseRows    = C.DQLITE_RESPONSE_ROWS
	ResponseEmpty   = C.DQLITE_RESPONSE_EMPTY
)

// Server is a Go wrapper arround dqlite_server.
type Server C.dqlite_server

// Init initializes dqlite global state.
func Init() error {
	var errmsg *C.char

	rc := C.dqlite_init(&errmsg)
	if rc != 0 {
		return fmt.Errorf("%s (%d)", C.GoString(errmsg), rc)
	}
	return nil
}

// NewServer creates a new Server instance.
func NewServer(cluster *Cluster) (*Server, error) {
	var server *C.dqlite_server

	rc := C.dqlite_server_create((*C.dqlite_cluster)(unsafe.Pointer(cluster)), &server)
	if rc != 0 {
		err := codeToError(rc)
		return nil, errors.Wrap(err, "failed to create server object")
	}

	return (*Server)(unsafe.Pointer(server)), nil
}

// Close the server releasing all used resources.
func (s *Server) Close() {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	C.dqlite_server_destroy(server)
}

// SetLogger sets the server logger.
func (s *Server) SetLogger(logger *Logger) {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	rc := C.dqlite_server_config(server, C.DQLITE_CONFIG_LOGGER, unsafe.Pointer(logger))
	if rc != 0 {
		// Setting the logger should never fail.
		panic("failed to set logger")
	}
}

// SetVfs sets the name of the VFS to use for new connections.
func (s *Server) SetVfs(name string) {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	rc := C.dqlite_server_config(server, C.DQLITE_CONFIG_VFS, unsafe.Pointer(cname))
	if rc != 0 {
		// Setting the logger should never fail.
		panic("failed to set vfs")
	}
}

// SetWalReplication sets the name of the WAL replication to use for new connections.
func (s *Server) SetWalReplication(name string) {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	rc := C.dqlite_server_config(server, C.DQLITE_CONFIG_WAL_REPLICATION, unsafe.Pointer(cname))
	if rc != 0 {
		// Setting the logger should never fail.
		panic("failed to set WAL replication")
	}
}

// Run the server.
//
// After this method is called it's possible to invoke Handle().
func (s *Server) Run() error {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	var errmsg *C.char

	rc := C.dqlite_server_run(server)
	if rc != 0 {
		return fmt.Errorf(C.GoString(errmsg))
	}

	return nil
}

// Ready waits for the server to be ready to handle connections.
func (s *Server) Ready() bool {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	return C.dqlite_server_ready(server) == 1
}

// Handle a new connection.
func (s *Server) Handle(conn net.Conn) error {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	file, err := conn.(fileConn).File()
	if err != nil {
		return err
	}
	defer file.Close()

	fd1 := C.int(file.Fd())

	// Duplicate the file descriptor, in order to prevent Go's finalizer to
	// close it.
	fd2 := C.dup_cloexec(fd1)
	if fd2 < 0 {
		return fmt.Errorf("failed to dup socket fd")
	}

	conn.Close()

	var errmsg *C.char

	rc := C.dqlite_server_handle(server, fd2, &errmsg)
	if rc != 0 {
		C.close(fd2)
		defer C.sqlite3_free(unsafe.Pointer(errmsg))
		if rc == C.DQLITE_STOPPED {
			return ErrServerStopped
		}
		return fmt.Errorf(C.GoString(errmsg))
	}

	return nil
}

// Interface that net.Conn must implement in order to extract the underlying
// file descriptor.
type fileConn interface {
	File() (*os.File, error)
}

// Stop the server.
func (s *Server) Stop() error {
	server := (*C.dqlite_server)(unsafe.Pointer(s))

	var errmsg *C.char

	rc := C.dqlite_server_stop(server, &errmsg)
	if rc != 0 {
		return fmt.Errorf(C.GoString(errmsg))
	}

	return nil
}

// ErrServerStopped is returned by Server.Handle() is the server was stopped.
var ErrServerStopped = fmt.Errorf("server was stopped")
