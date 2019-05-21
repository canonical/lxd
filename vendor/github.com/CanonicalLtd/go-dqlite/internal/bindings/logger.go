package bindings

/*
#include <assert.h>
#include <stdlib.h>

#include <dqlite.h>

// Silence warnings.
extern int vasprintf(char **strp, const char *fmt, va_list ap);

// Go land callback for xLogf.
void dqliteLoggerLogfCb(uintptr_t handle, int level, char *msg);

// Implementation of xLogf.
static void dqliteLoggerLogf(void *ctx, int level, const char *format, va_list args) {
  uintptr_t handle;
  char *msg;
  int err;

  assert(ctx != NULL);

  handle = (uintptr_t)ctx;

  err = vasprintf(&msg, format, args);
  if (err < 0) {
    // Ignore errors
    return;
  }

  dqliteLoggerLogfCb(handle, level, (char*)msg);

  free(msg);
}

// Constructor.
static dqlite_logger *dqlite__logger_create(uintptr_t handle) {
  dqlite_logger *logger = sqlite3_malloc(sizeof *logger);

  if (logger == NULL) {
    return NULL;
  }

  logger->data = (void*)handle;
  logger->emit = dqliteLoggerLogf;

  return logger;
}
*/
import "C"
import (
	"unsafe"

	"github.com/CanonicalLtd/go-dqlite/internal/logging"
)

// Logger is a Go wrapper around the associated dqlite's C type.
type Logger C.dqlite_logger

// Logging levels.
const (
	LogDebug = C.DQLITE_LOG_DEBUG
	LogInfo  = C.DQLITE_LOG_INFO
	LogWarn  = C.DQLITE_LOG_WARN
	LogError = C.DQLITE_LOG_ERROR
)

// NewLogger creates a new Logger object set with the given log function.
func NewLogger(f logging.Func) *Logger {
	// Register the logger implementation and pass its handle to
	// dqliteLoggerInit.
	handle := loggerFuncsSerial

	loggerFuncsIndex[handle] = f
	loggerFuncsSerial++

	logger := C.dqlite__logger_create(C.uintptr_t(handle))
	if logger == nil {
		panic("out of memory")
	}

	return (*Logger)(unsafe.Pointer(logger))
}

// Close releases all memory associated with the logger object.
func (l *Logger) Close() {
	logger := (*C.dqlite_logger)(unsafe.Pointer(l))
	handle := (C.uintptr_t)(uintptr(logger.data))

	delete(loggerFuncsIndex, handle)

	C.sqlite3_free(unsafe.Pointer(logger))
}

// Map uintptr to logging.Func instances to avoid passing Go pointers to C.
//
// We do not protect this map with a lock since typically just one long-lived
// Logger instance should be registered (except for unit tests).
var loggerFuncsSerial C.uintptr_t = 100
var loggerFuncsIndex = map[C.uintptr_t]logging.Func{}

//export dqliteLoggerLogfCb
func dqliteLoggerLogfCb(handle C.uintptr_t, level C.int, msg *C.char) {
	f := loggerFuncsIndex[handle]

	message := C.GoString(msg)
	switch level {
	case LogDebug:
		f(logging.Debug, message)
	case LogInfo:
		f(logging.Info, message)
	case LogWarn:
		f(logging.Warn, message)
	case LogError:
		f(logging.Error, message)
	}
}
