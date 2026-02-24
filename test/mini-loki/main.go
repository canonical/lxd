package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/canonical/lxd/test/mini-loki/serve"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
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

	errCh, err := serve.API(&loki{
		logfile: f,
	})
	if err != nil {
		return err
	}

	return <-errCh
}

type loki struct {
	logfile *os.File
}

func (l *loki) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/ready":
		w.WriteHeader(http.StatusOK)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/loki/api/v1/push":
		l.onPush(w, r)
		return
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
}

func (l *loki) onPush(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(l.logfile, r.Body)
	_, _ = l.logfile.WriteString("\n")
	w.WriteHeader(http.StatusOK)
}
