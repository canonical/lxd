package main

import (
	"net/http"
	"os"

	"github.com/grant-he/lxd/lxd/response"
	lxdshared "github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
	"github.com/grant-he/lxd/shared/version"
)

var api10Cmd = APIEndpoint{
	Get: APIEndpointAction{Handler: api10Get},
}

var api10 = []APIEndpoint{
	api10Cmd,
	execCmd,
	eventsCmd,
	fileCmd,
	operationsCmd,
	operationCmd,
	operationWebsocket,
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
