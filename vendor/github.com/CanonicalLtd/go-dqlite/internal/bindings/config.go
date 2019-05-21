package bindings

/*
#include <sqlite3.h>

// Wrapper around sqlite3_db_config() for invoking the
// SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE opcode, since there's no way to use C
// varargs from Go.
static int sqlite3__db_config_no_ckpt_on_close(sqlite3 *db, int value, int *pValue) {
  return sqlite3_db_config(db, SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE, value, pValue);
}
*/
import "C"
import (
	"unsafe"

	"github.com/pkg/errors"
)

// ConfigNoCkptOnClose switches on or off the automatic WAL checkpoint when a
// connection is closed.
func (c *Conn) ConfigNoCkptOnClose(flag bool) (bool, error) {
	db := (*C.sqlite3)(unsafe.Pointer(c))

	var in C.int
	var out C.int

	if flag {
		in = 1
	}

	rc := C.sqlite3__db_config_no_ckpt_on_close(db, in, &out)
	if rc != C.SQLITE_OK {
		err := lastError(db)
		return false, errors.Wrap(err, "failed to config checkpoint on close")
	}

	return out == 1, nil
}
