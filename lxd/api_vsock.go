package main

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/logger"
)

func vSockServer(d *Daemon) *http.Server {
	return &http.Server{Handler: devLXDAPI(d, hoistReqVM)}
}

func hoistReqVM(f func(*Daemon, instance.Instance, http.ResponseWriter, *http.Request) response.Response, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set devLXD auth method to identify this request as coming from the /dev/lxd socket.
		request.SetCtxValue(r, request.CtxProtocol, auth.AuthenticationMethodDevLXD)

		trusted, inst, err := authenticateAgentCert(d.State(), r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if !trusted {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}

		resp := f(d, inst, w, r)
		if resp != nil {
			err = resp.Render(w, r)
			if err != nil {
				writeErr := response.DevLXDErrorResponse(err, true).Render(w, r)
				if writeErr != nil {
					logger.Warn("Failed writing error for HTTP response", logger.Ctx{"url": r.URL, "err": err, "writeErr": writeErr})
				}
			}
		}
	}
}

func authenticateAgentCert(s *state.State, r *http.Request) (bool, instance.Instance, error) {
	var vsockID int
	trusted := false

	_, err := fmt.Sscanf(r.RemoteAddr, "vm(%d)", &vsockID)
	if err != nil {
		return false, nil, err
	}

	var clusterInst *cluster.Instance

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		clusterInst, err = tx.GetLocalInstanceWithVsockID(ctx, vsockID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return false, nil, err
	}

	inst, err := instance.LoadByProjectAndName(s, clusterInst.Project, clusterInst.Name)
	if err != nil {
		return false, nil, err
	}

	agentCert := inst.(instance.VM).AgentCertificate()
	trustedCerts := map[string]x509.Certificate{"0": *agentCert}

	for _, cert := range r.TLS.PeerCertificates {
		trusted, _ = util.CheckMutualTLS(*cert, trustedCerts)
		if trusted {
			return true, inst, nil
		}
	}

	return false, nil, nil
}
