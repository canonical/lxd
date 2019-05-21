package bindings

/*
#include <sqlite3.h>
#include <dqlite.h>
*/
import "C"

// SQLite datatype codes
const (
	Integer = C.SQLITE_INTEGER
	Float   = C.SQLITE_FLOAT
	Text    = C.SQLITE_TEXT
	Blob    = C.SQLITE_BLOB
	Null    = C.SQLITE_NULL
)

// Special data types for time values.
const (
	UnixTime = C.DQLITE_UNIXTIME
	ISO8601  = C.DQLITE_ISO8601
	Boolean  = C.DQLITE_BOOLEAN
)
