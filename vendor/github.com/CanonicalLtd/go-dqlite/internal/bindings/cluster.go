package bindings

/*
#include <assert.h>
#include <stdlib.h>

#include <dqlite.h>

// Go land callbacks for dqlite_cluster methods.
char *clusterLeaderCb(uintptr_t handle);
int clusterServersCb(uintptr_t handle, dqlite_server_info **servers);
void clusterRegisterCb(uintptr_t handle, sqlite3 *db);
void clusterUnregisterCb(uintptr_t handle, sqlite3 *db);
int clusterBarrierCb(uintptr_t handle);
int clusterRecoverCb(uintptr_t handle, uint64_t txToken);
int clusterCheckpointCb(uintptr_t handle, sqlite3 *db);

// Implementation of xLeader.
static const char* dqlite__cluster_leader(void *ctx) {
  assert(ctx != NULL);

  return clusterLeaderCb((uintptr_t)ctx);
}

// Implementation of xServers.
static int dqlite__cluster_servers(void *ctx, dqlite_server_info *servers[]) {
  assert(ctx != NULL);

  return clusterServersCb((uintptr_t)ctx, servers);
}

// Implementation of xRegister.
static void dqlite__cluster_register(void *ctx, sqlite3 *db) {
  assert(ctx != NULL);

  clusterRegisterCb((uintptr_t)ctx, db);
}

// Implementation of xUnregister.
static void dqlite__cluster_unregister(void *ctx, sqlite3 *db) {
  assert(ctx != NULL);

  clusterUnregisterCb((uintptr_t)ctx, db);
}

// Implementation of xBarrier.
static int dqlite__cluster_barrier(void *ctx) {
  assert(ctx != NULL);

  return clusterBarrierCb((uintptr_t)ctx);
}

// Implementation of of xRecover.
static int dqlite__cluster_recover(void *ctx, uint64_t tx_token) {
  assert(ctx != NULL);

  return clusterRecoverCb((uintptr_t)ctx, tx_token);
}

// Implementation of of xCheckpoint.
static int dqlite__cluster_checkpoint(void *ctx, sqlite3 *db) {
  assert(ctx != NULL);

  return clusterCheckpointCb((uintptr_t)ctx, db);
}

// Constructor.
static dqlite_cluster *dqlite__cluster_create(uintptr_t handle)
{
  dqlite_cluster *c = sqlite3_malloc(sizeof *c);
  if (c == NULL) {
    return NULL;
  }

  c->ctx = (void*)handle;
  c->xLeader = dqlite__cluster_leader;
  c->xServers = dqlite__cluster_servers;
  c->xRegister = dqlite__cluster_register;
  c->xUnregister = dqlite__cluster_unregister;
  c->xBarrier = dqlite__cluster_barrier;
  c->xRecover = dqlite__cluster_recover;
  c->xCheckpoint = dqlite__cluster_checkpoint;

  return c;
}
*/
import "C"
import (
	"unsafe"
)

// Cluster is a Go wrapper around the associated dqlite's C type.
type Cluster C.dqlite_cluster

// NewCluster creates a new Cluster object set with the given method hooks..
func NewCluster(methods ClusterMethods) (*Cluster, error) {
	handle := clusterMethodsSerial
	clusterMethodsIndex[handle] = methods
	clusterMethodsSerial++

	cluster := C.dqlite__cluster_create(handle)
	if cluster == nil {
		return nil, codeToError(C.SQLITE_NOMEM)
	}

	return (*Cluster)(unsafe.Pointer(cluster)), nil
}

// Close releases all memory associated with the cluster object.
func (c *Cluster) Close() {
	cluster := (*C.dqlite_cluster)(unsafe.Pointer(c))

	handle := (C.uintptr_t)(uintptr(cluster.ctx))
	delete(clusterMethodsIndex, handle)

	C.sqlite3_free(unsafe.Pointer(cluster))
}

// ServerInfo is the Go equivalent of dqlite_server_info.
type ServerInfo struct {
	ID      uint64
	Address string
}

// ClusterMethods implements the interface required by the various hooks
// dqlite_cluster.
type ClusterMethods interface {
	// Return the address of the current cluster leader, if any. If not
	// empty, the address string must a be valid network IP or hostname,
	// that clients can use to connect to a dqlite service.
	Leader() string

	// If this driver is the current leader of the cluster, return the
	// addresses of all other servers. Each address must be a valid IP or
	// host name name, that clients can use to connect to the relevant
	// dqlite service , in case the current leader is deposed and a new one
	// is elected.
	//
	// If this driver is not the current leader of the cluster, an error
	// implementing the Error interface below and returning true in
	// NotLeader() must be returned.
	Servers() ([]ServerInfo, error)

	Register(*Conn)
	Unregister(*Conn)

	Barrier() error

	Recover(token uint64) error

	Checkpoint(*Conn) error
}

// Map uintptr to Cluster instances to avoid passing Go pointers to C.
//
// We do not protect this map with a lock since typically just one long-lived
// Cluster instance should be registered (except for unit tests).
var clusterMethodsSerial C.uintptr_t = 100
var clusterMethodsIndex = map[C.uintptr_t]ClusterMethods{}

//export clusterLeaderCb
func clusterLeaderCb(handle C.uintptr_t) *C.char {
	cluster := clusterMethodsIndex[handle]

	// It's responsibility of calling code to free() this string.
	return C.CString(cluster.Leader())
}

//export clusterServersCb
func clusterServersCb(handle C.uintptr_t, out **C.dqlite_server_info) C.int {
	cluster := clusterMethodsIndex[handle]

	servers, err := cluster.Servers()
	if err != nil {
		*out = nil
		return C.int(ErrorCode(err))
	}

	n := C.size_t(len(servers)) + 1

	// It's responsibility of calling code to free() this array of servers.
	size := unsafe.Sizeof(C.dqlite_server_info{})
	*out = (*C.dqlite_server_info)(C.malloc(n * C.size_t(size)))

	if *out == nil {
		return C.SQLITE_NOMEM
	}

	for i := C.size_t(0); i < n; i++ {
		server := (*C.dqlite_server_info)(unsafe.Pointer(uintptr(unsafe.Pointer(*out)) + size*uintptr(i)))

		if i == n-1 {
			server.id = 0
			server.address = nil
		} else {
			server.id = C.uint64_t(servers[i].ID)
			server.address = C.CString(servers[i].Address)
		}
	}

	return C.int(0)
}

//export clusterRegisterCb
func clusterRegisterCb(handle C.uintptr_t, db *C.sqlite3) {
	cluster := clusterMethodsIndex[handle]
	cluster.Register((*Conn)(unsafe.Pointer(db)))
}

//export clusterUnregisterCb
func clusterUnregisterCb(handle C.uintptr_t, db *C.sqlite3) {
	cluster := clusterMethodsIndex[handle]
	cluster.Unregister((*Conn)(unsafe.Pointer(db)))
}

//export clusterBarrierCb
func clusterBarrierCb(handle C.uintptr_t) C.int {
	cluster := clusterMethodsIndex[handle]

	if err := cluster.Barrier(); err != nil {
		return C.int(ErrorCode(err))
	}

	return 0
}

//export clusterRecoverCb
func clusterRecoverCb(handle C.uintptr_t, txToken C.uint64_t) C.int {
	cluster := clusterMethodsIndex[handle]

	err := cluster.Recover(uint64(txToken))
	if err != nil {
		return C.int(ErrorCode(err))
	}

	return C.int(0)
}

//export clusterCheckpointCb
func clusterCheckpointCb(handle C.uintptr_t, db *C.sqlite3) C.int {
	cluster := clusterMethodsIndex[handle]

	err := cluster.Checkpoint((*Conn)(unsafe.Pointer(db)))
	if err != nil {
		return C.int(ErrorCode(err))
	}

	return C.int(0)
}
