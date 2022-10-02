package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/lxc/lxd/client"
	agentAPI "github.com/lxc/lxd/lxd-agent/api"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/vsock"
	lxdshared "github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var api10Cmd = APIEndpoint{
	Get: APIEndpointAction{Handler: api10Get},
	Put: APIEndpointAction{Handler: api10Put},
}

var api10 = []APIEndpoint{
	api10Cmd,
	execCmd,
	eventsCmd,
	metricsCmd,
	operationsCmd,
	operationCmd,
	operationWebsocket,
	sftpCmd,
	stateCmd,
}

func api10Get(d *Daemon, r *http.Request) response.Response {
	srv := api.ServerUntrusted{
		APIExtensions: version.APIExtensions,
		APIStatus:     "stable",
		APIVersion:    version.APIVersion,
		Public:        false,
		Auth:          "trusted",
		AuthMethods:   []string{"tls"},
	}

	uname, err := lxdshared.Uname()
	if err != nil {
		return response.InternalError(err)
	}

	serverName, err := os.Hostname()
	if err != nil {
		return response.SmartError(err)
	}

	env := api.ServerEnvironment{
		Kernel:             uname.Sysname,
		KernelArchitecture: uname.Machine,
		KernelVersion:      uname.Release,
		Server:             "lxd-agent",
		ServerPid:          os.Getpid(),
		ServerVersion:      version.Version,
		ServerName:         serverName,
	}

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env

	return response.SyncResponseETag(true, fullSrv, fullSrv)
}

func api10Put(d *Daemon, r *http.Request) response.Response {
	var data agentAPI.API10Put

	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	d.devlxdMu.Lock()
	d.serverCID = data.CID
	d.serverPort = data.Port
	d.serverCertificate = data.Certificate
	d.devlxdMu.Unlock()

	// Try connecting to LXD server.
	client, err := getClient(int(d.serverCID), int(d.serverPort), d.serverCertificate)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	server, err := lxd.ConnectLXDHTTP(nil, client)
	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	defer server.Disconnect()

	// Let LXD know, we were able to connect successfully.
	d.chConnected <- struct{}{}

	if data.Devlxd {
		err = startDevlxdServer(d)
	} else {
		err = stopDevlxdServer(d)
	}

	if err != nil {
		return response.ErrorResponse(http.StatusInternalServerError, err.Error())
	}

	return response.EmptySyncResponse
}

func startDevlxdServer(d *Daemon) error {
	d.devlxdMu.Lock()
	defer d.devlxdMu.Unlock()

	// If a devlxd server is already running, don't start a second one.
	if d.devlxdRunning {
		return nil
	}

	servers["devlxd"] = devLxdServer(d)

	// Prepare the devlxd server.
	devlxdListener, err := createDevLxdlListener("/dev")
	if err != nil {
		return err
	}

	d.devlxdRunning = true

	// Start the devlxd listener.
	go func() {
		err := servers["devlxd"].Serve(devlxdListener)
		if err != nil {
			d.devlxdMu.Lock()
			d.devlxdRunning = false
			d.devlxdMu.Unlock()

			// http.ErrServerClosed can be ignored as this is returned when the server is closed intentionally.
			if !errors.Is(err, http.ErrServerClosed) {
				errChan <- err
			}
		}
	}()

	return nil
}

func stopDevlxdServer(d *Daemon) error {
	d.devlxdMu.Lock()
	d.devlxdRunning = false
	d.devlxdMu.Unlock()

	return servers["devlxd"].Close()
}

func getClient(CID int, port int, serverCertificate string) (*http.Client, error) {
	agentCert, err := os.ReadFile("agent.crt")
	if err != nil {
		return nil, err
	}

	agentKey, err := os.ReadFile("agent.key")
	if err != nil {
		return nil, err
	}

	client, err := vsock.HTTPClient(CID, port, string(agentCert), string(agentKey), serverCertificate)
	if err != nil {
		return nil, err
	}

	return client, nil
}
