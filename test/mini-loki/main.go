package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/canonical/lxd/test/testutils/servemock"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error()+"\n")
		os.Exit(1)
	}
}

func run() (err error) {
	var workdir string
	if len(os.Args) > 1 {
		workdir = os.Args[1]
	} else {
		workdir, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	f, err := os.Create(filepath.Join(workdir, "loki.logs"))
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
	}()

	l := &loki{logfile: f}

	result, err := servemock.API(context.Background(), servemock.Config{
		Address:  "127.0.0.1:3100",
		Handlers: l.handlers(),
	})
	if err != nil {
		return err
	}

	return <-result.Err
}

type loki struct {
	logfile *os.File
}

func (l *loki) handlers() []servemock.Handler {
	return []servemock.Handler{l.ready(), l.push()}
}

func (l *loki) ready() servemock.Handler {
	return servemock.Handler{
		Pattern: "GET /ready",
		HTTPHandler: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	}
}

func (l *loki) push() servemock.Handler {
	return servemock.Handler{
		Pattern: "POST /loki/api/v1/push",
		HTTPHandler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(l.logfile, r.Body)
			_, _ = l.logfile.WriteString("\n")
			w.WriteHeader(http.StatusOK)
		},
	}
}
