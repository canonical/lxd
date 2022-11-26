package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/tcp"
)

// swagger:operation GET /1.0/instances/{name}/sftp instances instance_sftp
//
// Get the instance SFTP connection
//
// Upgrades the request to an SFTP connection of the instance's filesystem.
//
// ---
// produces:
//   - application/json
//   - application/octet-stream
// responses:
//   "101":
//     description: Switching protocols to SFTP
//   "400":
//     $ref: "#/responses/BadRequest"
//   "404":
//     $ref: "#/responses/NotFound"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instanceSFTPHandler(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)
	instName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(instName) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	if r.Header.Get("Upgrade") != "sftp" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	// Redirect to correct server if needed.
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	resp := &sftpServeResponse{
		req:         r,
		d:           d,
		projectName: projectName,
		instName:    instName,
	}

	// Forward the request if the instance is remote.
	client, err := cluster.ConnectIfInstanceIsRemote(d.db.Cluster, projectName, instName, d.endpoints.NetworkCert(), d.serverCert(), r, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		resp.instConn, err = client.GetInstanceFileSFTPConn(instName)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		inst, err := instance.LoadByProjectAndName(d.State(), projectName, instName)
		if err != nil {
			return response.SmartError(err)
		}

		resp.instConn, err = inst.FileSFTPConn()
		if err != nil {
			return response.SmartError(api.StatusErrorf(http.StatusInternalServerError, "Failed getting instance SFTP connection: %v", err))
		}
	}

	return resp
}

type sftpServeResponse struct {
	req         *http.Request
	d           *Daemon
	projectName string
	instName    string
	instConn    net.Conn
}

func (r *sftpServeResponse) String() string {
	return "sftp handler"
}

func (r *sftpServeResponse) Render(w http.ResponseWriter) error {
	defer func() { _ = r.instConn.Close() }()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return api.StatusErrorf(http.StatusInternalServerError, "Webserver doesn't support hijacking")
	}

	remoteConn, _, err := hijacker.Hijack()
	if err != nil {
		return api.StatusErrorf(http.StatusInternalServerError, "Failed to hijack connection: %v", err)
	}

	defer func() { _ = remoteConn.Close() }()

	remoteTCP, _ := tcp.ExtractConn(remoteConn)
	if remoteTCP != nil {
		// Apply TCP timeouts if remote connection is TCP (rather than Unix).
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			return api.StatusErrorf(http.StatusInternalServerError, "Failed setting TCP timeouts on remote connection: %v", err)
		}
	}

	err = response.Upgrade(remoteConn, "sftp")
	if err != nil {
		return api.StatusErrorf(http.StatusInternalServerError, err.Error())
	}

	ctx, cancel := context.WithCancel(r.req.Context())
	l := logger.AddContext(logger.Log, logger.Ctx{
		"project":  r.projectName,
		"instance": r.instName,
		"local":    remoteConn.LocalAddr(),
		"remote":   remoteConn.RemoteAddr(),
		"err":      err,
	})

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(remoteConn, r.instConn)
		if err != nil {
			if ctx.Err() == nil {
				l.Warn("Failed copying SFTP instance connection to remote connection", logger.Ctx{"err": err})
			}
		}
		cancel()               // Cancel context first so when remoteConn is closed it doesn't cause a warning.
		_ = remoteConn.Close() // Trigger the cancellation of the io.Copy reading from remoteConn.
	}()

	_, err = io.Copy(r.instConn, remoteConn)
	if err != nil {
		if ctx.Err() == nil {
			l.Warn("Failed copying SFTP remote connection to instance connection", logger.Ctx{"err": err})
		}
	}
	cancel() // Cancel context first so when instConn is closed it doesn't cause a warning.

	err = r.instConn.Close() // Trigger the cancellation of the io.Copy reading from instConn.
	if err != nil {
		return fmt.Errorf("Failed closing connection to remote server: %w", err)
	}

	wg.Wait() // Wait for copy go routine to finish.

	return nil
}
