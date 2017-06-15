package operation

import (
	"net/http"
)

// An operation that can be canceled.
type CancellableOperation interface {

	// Toggle whether the operation is cancellable
	Cancellable(flag bool) chan error
}

func CancellableDownload(op CancellableOperation, client *http.Client, req *http.Request) (*http.Response, error, chan bool) {
	chDone := make(chan bool)

	go func() {
		chCancel := op.Cancellable(true)
		select {
		case <-chCancel:
			if transport, ok := client.Transport.(*http.Transport); ok {
				transport.CancelRequest(req)
			}
		case <-chDone:
		}
		op.Cancellable(false)
	}()

	resp, err := client.Do(req)
	return resp, err, chDone
}
