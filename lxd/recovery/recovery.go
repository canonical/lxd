package recovery

// Panic is a channel that a single PanicResult can be sent on. This channel is listened to in the `lxd` daemon command
// and if received, the daemon exits. We are doing this rather than panicking directly in case any libraries implement
// their own panic recovery. For example, we need to bypass the recovery in the standard net/http library (see https://github.com/golang/go/issues/25245).
var Panic = make(chan PanicResult, 1)

// PanicResult contains the panic cause and a stacktrace.
type PanicResult struct {
	Err        error
	Stacktrace []byte
}
