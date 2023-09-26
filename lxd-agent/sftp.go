package main

import (
	"fmt"
	"net/http"

	"github.com/pkg/sftp"

	"github.com/canonical/lxd/lxd/response"
)

var sftpCmd = APIEndpoint{
	Name: "sftp",
	Path: "sftp",

	Get: APIEndpointAction{Handler: sftpHandler},
}

// sftpHandler upgrades the HTTP connection to SFTP and starts the SFTP server.
func sftpHandler(d *Daemon, r *http.Request) response.Response {
	return &sftpServe{d, r}
}

type sftpServe struct {
	d *Daemon
	r *http.Request
}

// String returns a descriptive name for the sftpServe handler.
func (r *sftpServe) String() string {
	return "sftp handler"
}

// Render starts the SFTP server to handle requests after upgrading the connection.
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
		http.Error(w, fmt.Errorf("Failed to hijack connection: %w", err).Error(), http.StatusInternalServerError)

		return nil
	}

	defer func() { _ = conn.Close() }()

	err = response.Upgrade(conn, "sftp")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return nil
	}

	// Start sftp server.
	server, err := sftp.NewServer(conn)
	if err != nil {
		return nil
	}

	return server.Serve()
}
