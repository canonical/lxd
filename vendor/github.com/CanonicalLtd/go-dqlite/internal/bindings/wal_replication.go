package bindings

/*
#include <stdint.h>
#include <stdlib.h>
#include <sqlite3.h>
#include <string.h>

// WAL replication trampolines.
int walReplicationBegin(uintptr_t handle, sqlite3 *db);
int walReplicationAbort(uintptr_t handle, sqlite3 *db);
int walReplicationFrames(uintptr_t handle, sqlite3 *db,
      int, int, sqlite3_wal_replication_frame*, unsigned, int);
int walReplicationUndo(uintptr_t handle, sqlite3 *db);
int walReplicationEnd(uintptr_t handle, sqlite3 *db);

// Wal replication methods.
static int sqlite3__wal_replication_begin(sqlite3_wal_replication *r, void *arg)
{
  uintptr_t handle = (uintptr_t)(r->pAppData);
  sqlite3 *db = (sqlite3*)(arg);
  return walReplicationBegin(handle, db);
}

static int sqlite3__wal_replication_abort(sqlite3_wal_replication *r, void *arg)
{
  uintptr_t handle = (uintptr_t)(r->pAppData);
  sqlite3 *db = (sqlite3*)(arg);
  return walReplicationAbort(handle, db);
}

static int sqlite3__wal_replication_frames(sqlite3_wal_replication *r, void *arg,
  int szPage, int nFrame, sqlite3_wal_replication_frame *aFrame,
  unsigned nTruncate, int isCommit)
{
  uintptr_t handle = (uintptr_t)(r->pAppData);
  sqlite3 *db = (sqlite3*)(arg);
  return walReplicationFrames(handle, db, szPage, nFrame, aFrame, nTruncate, isCommit);
}

static int sqlite3__wal_replication_undo(sqlite3_wal_replication *r, void *arg)
{
  uintptr_t handle = (uintptr_t)(r->pAppData);
  sqlite3 *db = (sqlite3*)(arg);
  return walReplicationUndo(handle, db);
}

static int sqlite3__wal_replication_end(sqlite3_wal_replication *r, void *arg)
{
  uintptr_t handle = (uintptr_t)(r->pAppData);
  sqlite3 *db = (sqlite3*)(arg);
  return walReplicationEnd(handle, db);
}

// Constructor.
static sqlite3_wal_replication *sqlite3__wal_replication_create(char *name, uintptr_t ctx){
  sqlite3_wal_replication *replication;

  replication = sqlite3_malloc(sizeof *replication);
  if (replication == NULL) {
    goto oom;
  }

  replication->iVersion = 1;

  // Copy the name so the Go side can just free it.
  replication->zName    = sqlite3_malloc(strlen(name));
  if (replication->zName == NULL) {
    goto oom_after_replication_malloc;
  }
  strcpy((char *)replication->zName, (const char*)name);

  replication->pAppData = (void*)ctx;
  replication->xBegin   = sqlite3__wal_replication_begin;
  replication->xAbort   = sqlite3__wal_replication_abort;
  replication->xFrames  = sqlite3__wal_replication_frames;
  replication->xUndo    = sqlite3__wal_replication_undo;
  replication->xEnd     = sqlite3__wal_replication_end;

  return replication;

oom_after_replication_malloc:
  sqlite3_free(replication);

oom:
  return NULL;
}

// Destructor.
static void sqlite3__wal_replication_destroy(sqlite3_wal_replication *replication) {
  sqlite3_free((char *)replication->zName);
  sqlite3_free(replication);
}

*/
import "C"
import (
	"unsafe"
)

// WalReplication is a Go wrapper around the associated SQLite's C type.
type WalReplication C.sqlite3_wal_replication

// NewWalReplication registers a WAL replication instance under the given
// name.
func NewWalReplication(name string, methods WalReplicationMethods) (*WalReplication, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	if r := C.sqlite3_wal_replication_find(cname); r != nil {
		err := Error{Code: C.SQLITE_ERROR, Message: "WAL replication name already registered"}
		return nil, err
	}

	handle := walReplicationMethodsSerial
	walReplicationMethodsIndex[handle] = methods
	walReplicationMethodsSerial++

	replication := C.sqlite3__wal_replication_create(cname, handle)
	if replication == nil {
		return nil, codeToError(C.SQLITE_NOMEM)
	}

	rc := C.sqlite3_wal_replication_register(replication, 0)
	if rc != 0 {
		return nil, codeToError(rc)
	}

	return (*WalReplication)(unsafe.Pointer(replication)), nil
}

// Name returns the registration name of the Wal replication.
func (r *WalReplication) Name() string {
	replication := (*C.sqlite3_wal_replication)(unsafe.Pointer(r))

	return C.GoString(replication.zName)
}

// Close unregisters and destroys this WAL replication instance.
func (r *WalReplication) Close() error {
	replication := (*C.sqlite3_wal_replication)(unsafe.Pointer(r))

	rc := C.sqlite3_wal_replication_unregister(replication)
	if rc != 0 {
		return codeToError(rc)
	}

	handle := (C.uintptr_t)(uintptr(replication.pAppData))
	delete(walReplicationMethodsIndex, handle)

	C.sqlite3__wal_replication_destroy(replication)

	return nil
}

// WalReplicationLeader switches the SQLite connection to leader WAL
// replication mode.
func (c *Conn) WalReplicationLeader(name string) error {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	rc := C.sqlite3_wal_replication_leader(db, walReplicationSchema, cname, unsafe.Pointer(db))
	if rc != C.SQLITE_OK {
		return lastError(db)
	}

	return nil
}

// WalReplicationFollower switches the given SQLite connection to follower WAL
// replication mode. In this mode no regular operation is possible, and the
// connection should be driven with the WalReplicationFrames, and
// WalReplicationUndo APIs.
func (c *Conn) WalReplicationFollower() error {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	rc := C.sqlite3_wal_replication_follower(db, walReplicationSchema)
	if rc != C.SQLITE_OK {
		return lastError(db)
	}

	return nil
}

// WalReplicationFrames writes the given batch of frames to the write-ahead log
// linked to the given connection.
//
// This method must be called with a "follower" connection, meant to replicate
// the "leader" one.
func (c *Conn) WalReplicationFrames(info WalReplicationFrameInfo) error {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	rc := C.sqlite3_wal_replication_frames(
		db, walReplicationSchema, info.isBegin, info.szPage, info.nFrame,
		info.aPgno, info.aPage, info.nTruncate, info.isCommit)
	if rc != C.SQLITE_OK {
		return lastError(db)
	}

	return nil
}

// WalReplicationUndo rollbacks a write transaction in the given sqlite
// connection. This should be called with a "follower" connection, meant to
// replicate the "leader" one.
func (c *Conn) WalReplicationUndo() error {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	rc := C.sqlite3_wal_replication_undo(db, walReplicationSchema)
	if rc != C.SQLITE_OK {
		return lastError(db)
	}
	return nil
}

// WalReplicationMethods implements the interface required by the various hooks
// of sqlite3_wal_replication.
type WalReplicationMethods interface {
	// Begin a new write transaction. The implementation should check
	// that the database is eligible for starting a replicated write
	// transaction (e.g. this node is the leader), and perform internal
	// state changes as appropriate.
	Begin(*Conn) int

	// Abort a write transaction. The implementation should clear any
	// state previously set by the Begin hook.
	Abort(*Conn) int

	// Write new frames to the write-ahead log. The implementation should
	// broadcast this write to other nodes and wait for a quorum.
	Frames(*Conn, WalReplicationFrameList) int

	// Undo a write transaction. The implementation should broadcast
	// this event to other nodes and wait for a quorum. The return code
	// is currently ignored by SQLite.
	Undo(*Conn) int

	// End a write transaction. The implementation should update its
	// internal state and be ready for a new transaction.
	End(*Conn) int
}

// PageNumber identifies a single database or WAL page.
type PageNumber C.unsigned

// FrameNumber identifies a single frame in the WAL.
type FrameNumber C.unsigned

// WalReplicationFrameList holds information about a single batch of WAL frames
// that are being dispatched for replication by a leader connection.
//
// They map to the parameters of the sqlite3_wal_replication.xFrames API
type WalReplicationFrameList struct {
	szPage    C.int
	nFrame    C.int
	aFrame    *C.sqlite3_wal_replication_frame
	nTruncate C.uint
	isCommit  C.int
}

// PageSize returns the page size of this batch of WAL frames.
func (l *WalReplicationFrameList) PageSize() int {
	return int(l.szPage)
}

// Len returns the number of WAL frames in this batch.
func (l *WalReplicationFrameList) Len() int {
	return int(l.nFrame)
}

// Truncate returns the size of the database in pages after this batch of WAL
// frames is applied.
func (l *WalReplicationFrameList) Truncate() uint {
	return uint(l.nTruncate)
}

// Frame returns information about the i'th frame in the batch.
func (l *WalReplicationFrameList) Frame(i int) (unsafe.Pointer, PageNumber, FrameNumber) {
	pFrame := (*C.sqlite3_wal_replication_frame)(unsafe.Pointer(
		uintptr(unsafe.Pointer(l.aFrame)) +
			unsafe.Sizeof(*l.aFrame)*uintptr(i),
	))
	return pFrame.pBuf, PageNumber(pFrame.pgno), FrameNumber(pFrame.iPrev)
}

// IsCommit returns whether this batch of WAL frames concludes a transaction.
func (l *WalReplicationFrameList) IsCommit() bool {
	return l.isCommit > 0
}

// WalReplicationFrameInfo information about a single batch of WAL frames that
// are being replicated by a follower connection.
type WalReplicationFrameInfo struct {
	isBegin   C.int
	szPage    C.int
	nFrame    C.int
	aPgno     *C.unsigned
	aPage     unsafe.Pointer
	nTruncate C.uint
	isCommit  C.int
}

// IsBegin sets the C isBegin parameter for sqlite3_wal_replication_frames.
func (i *WalReplicationFrameInfo) IsBegin(flag bool) {
	if flag {
		i.isBegin = C.int(1)
	} else {
		i.isBegin = C.int(0)
	}
}

// PageSize sets the C szPage parameter for sqlite3_wal_replication_frames.
func (i *WalReplicationFrameInfo) PageSize(size int) {
	i.szPage = C.int(size)
}

// Len sets the C nFrame parameter for sqlite3_wal_replication_frames.
func (i *WalReplicationFrameInfo) Len(n int) {
	i.nFrame = C.int(n)
}

// Pages sets the C aPgno and aPage parameters for sqlite3_wal_replication_frames.
func (i *WalReplicationFrameInfo) Pages(numbers []PageNumber, data unsafe.Pointer) {
	i.aPgno = (*C.unsigned)(&numbers[0])
	i.aPage = data
}

// Truncate sets the nTruncate parameter for sqlite3_wal_replication_frames.
func (i *WalReplicationFrameInfo) Truncate(truncate uint) {
	i.nTruncate = C.unsigned(truncate)
}

// IsCommit sets the isCommit parameter for sqlite3_wal_replication_frames.
func (i *WalReplicationFrameInfo) IsCommit(flag bool) {
	if flag {
		i.isCommit = C.int(1)
	} else {
		i.isCommit = C.int(0)
	}
}

func (i *WalReplicationFrameInfo) IsCommitGet() bool {
	return i.isCommit > 0
}

//export walReplicationBegin
func walReplicationBegin(handle C.uintptr_t, db *C.sqlite3) C.int {
	methods := walReplicationMethodsIndex[handle]

	return C.int(methods.Begin((*Conn)(unsafe.Pointer(db))))
}

//export walReplicationAbort
func walReplicationAbort(handle C.uintptr_t, db *C.sqlite3) C.int {
	methods := walReplicationMethodsIndex[handle]
	return C.int(methods.Abort((*Conn)(unsafe.Pointer(db))))
}

//export walReplicationFrames
func walReplicationFrames(
	handle C.uintptr_t,
	db *C.sqlite3,
	szPage C.int,
	nFrame C.int,
	aFrame *C.sqlite3_wal_replication_frame,
	nTruncate C.uint,
	isCommit C.int,
) C.int {
	methods := walReplicationMethodsIndex[handle]

	list := WalReplicationFrameList{
		szPage:    szPage,
		nFrame:    nFrame,
		aFrame:    aFrame,
		nTruncate: nTruncate,
		isCommit:  isCommit,
	}

	return C.int(methods.Frames((*Conn)(unsafe.Pointer(db)), list))
}

//export walReplicationUndo
func walReplicationUndo(handle C.uintptr_t, db *C.sqlite3) C.int {
	methods := walReplicationMethodsIndex[handle]

	return C.int(methods.Undo((*Conn)(unsafe.Pointer(db))))
}

//export walReplicationEnd
func walReplicationEnd(handle C.uintptr_t, db *C.sqlite3) C.int {
	methods := walReplicationMethodsIndex[handle]

	return C.int(methods.End((*Conn)(unsafe.Pointer(db))))
}

// Map uintptr to WalReplicationMethods instances to avoid passing Go pointers
// to C.
//
// We do not protect this map with a lock since typically just one long-lived
// WalReplication instance should be registered (except for unit tests).
var walReplicationMethodsSerial C.uintptr_t = 100
var walReplicationMethodsIndex = map[C.uintptr_t]WalReplicationMethods{}

// Hard-coded main schema name.
//
// TODO: support replicating also attached databases.
var walReplicationSchema = C.CString("main")
