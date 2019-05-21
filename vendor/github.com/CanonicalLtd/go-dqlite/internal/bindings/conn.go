package bindings

/*
#include <stdlib.h>
#include <sqlite3.h>
*/
import "C"
import (
	"database/sql/driver"
	"io"
	"unsafe"
)

// Open modes.
const (
	OpenReadWrite = C.SQLITE_OPEN_READWRITE
	OpenReadOnly  = C.SQLITE_OPEN_READONLY
	OpenCreate    = C.SQLITE_OPEN_CREATE
)

// Conn is a Go wrapper around a SQLite database handle.
type Conn C.sqlite3

// Open a SQLite connection.
func Open(name string, vfs string) (*Conn, error) {
	flags := OpenReadWrite | OpenCreate

	// Open the database.
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cvfs := C.CString(vfs)
	defer C.free(unsafe.Pointer(cvfs))

	var db *C.sqlite3

	rc := C.sqlite3_open_v2(cname, &db, C.int(flags), cvfs)
	if rc != C.SQLITE_OK {
		err := lastError(db)
		C.sqlite3_close_v2(db)
		return nil, err
	}

	return (*Conn)(unsafe.Pointer(db)), nil
}

// Close the connection.
func (c *Conn) Close() error {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	rc := C.sqlite3_close(db)
	if rc != C.SQLITE_OK {
		return lastError(db)
	}

	return nil
}

// Filename of the underlying database file.
func (c *Conn) Filename() string {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	return C.GoString(C.sqlite3_db_filename(db, walReplicationSchema))
}

// Exec executes a query.
func (c *Conn) Exec(query string) error {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	sql := C.CString(query)
	defer C.free(unsafe.Pointer(sql))

	rc := C.sqlite3_exec(db, sql, nil, nil, nil)
	if rc != C.SQLITE_OK {
		return lastError(db)
	}

	return nil
}

// Query the database.
func (c *Conn) Query(query string) (*Rows, error) {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	var stmt *C.sqlite3_stmt
	var tail *C.char

	sql := C.CString(query)
	defer C.free(unsafe.Pointer(sql))

	rc := C.sqlite3_prepare(db, sql, C.int(-1), &stmt, &tail)
	if rc != C.SQLITE_OK {
		return nil, lastError(db)
	}

	rows := &Rows{db: db, stmt: stmt}

	return rows, nil
}

// Rows represents a result set.
type Rows struct {
	db   *C.sqlite3
	stmt *C.sqlite3_stmt
}

// Next fetches the next row of a result set.
func (r *Rows) Next(values []driver.Value) error {
	rc := C.sqlite3_step(r.stmt)
	if rc == C.SQLITE_DONE {
		return io.EOF
	}
	if rc != C.SQLITE_ROW {
		return lastError(r.db)
	}

	for i := range values {
		values[i] = int64(C.sqlite3_column_int64(r.stmt, C.int(i)))
	}

	return nil
}

// Close finalizes the underlying statement.
func (r *Rows) Close() error {
	rc := C.sqlite3_finalize(r.stmt)
	if rc != C.SQLITE_OK {
		return lastError(r.db)
	}

	return nil
}
