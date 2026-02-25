package serve

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

// API starts an HTTP server with the given handler and returns the address it's
// listening on, a channel for errors, and any error encountered while starting
// the server.
func API(handler http.Handler) (<-chan error, error) {
	mux := http.NewServeMux()
	mux.Handle("/{any...}", handler)

	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, unix.SIGINT, unix.SIGKILL)

	l, err := net.Listen("tcp", "127.0.0.1:3100")
	if err != nil {
		return nil, err
	}

	s := &http.Server{Handler: handler}

	errCh := make(chan error)
	go func() {
		err = s.Serve(l)

		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	go func() {
		<-sigchan
		_ = s.Close()
	}()

	return errCh, nil
}
