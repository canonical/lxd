package serve

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

func API(certfile, keyfile string, handler http.Handler) (net.Addr, <-chan error, error) {
	mux := http.NewServeMux()
	mux.Handle("/{any...}", handler)

	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, unix.SIGINT, unix.SIGKILL)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}

	s := &http.Server{Handler: handler}

	errCh := make(chan error)
	go func() {
		var err error
		if certfile != "" {
			err = s.ServeTLS(l, certfile, keyfile)
		} else {
			err = s.Serve(l)
		}

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

	return l.Addr(), errCh, nil
}
