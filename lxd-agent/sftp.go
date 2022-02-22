package main

import (
	"fmt"
	"net/http"

	"github.com/pkg/sftp"

	"github.com/lxc/lxd/lxd/response"
)

var sftpCmd = APIEndpoint{
	Name: "sftp",
	Path: "sftp",

	Get: APIEndpointAction{Handler: sftpHandler},
}

func sftpHandler(d *Daemon, r *http.Request) response.Response {
	return &sftpServe{d, r}
}

type sftpServe struct {
	d *Daemon
	r *http.Request
}

func (r *sftpServe) String() string {
	return "sftp handler"
}

func (r *sftpServe) Render(w http.ResponseWriter) error {
	// Upgrade to sftp.
	if r.r.Header.Get("Upgrade") != "sftp" {
		http.Error(w, "Missing or invalid upgrade header", http.StatusBadRequest)
		return nil
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Webserver doesn't support hijacking", http.StatusInternalServerError)
		return nil
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to hijack connection: %v", err), http.StatusInternalServerError)
		return nil
	}
	defer conn.Close()

	data := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: sftp\r\n\r\n")
	n, err := conn.Write(data)
	if err != nil || n != len(data) {
		return nil
	}

	// Start sftp server.
	server, err := sftp.NewServer(conn)
	if err != nil {
		return nil
	}

	return server.Serve()
}
