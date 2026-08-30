package main

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance"
	instanceDrivers "github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/api"
)

// vSockServer creates an http.Server capable of handling /dev/lxd requests over vsock.
func vSockServer(d *Daemon) *http.Server {
	return &http.Server{
		Handler:           devLXDAPI(d, vSockAuthenticator{}),
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: util.HTTPServerReadTimeout,
		ReadTimeout:       util.HTTPServerReadTimeout,
	}
}

// vSockAuthenticator implements DevLXDAuthenticator for vsock connections.
type vSockAuthenticator struct{}

// authenticateMicroVMAgentCertFunc is the MicroVM agent authenticator, replaceable in unit tests.
var authenticateMicroVMAgentCertFunc = authenticateMicroVMAgentCert

// IsVsock returns true indicating that this authenticator is used for vsock connections.
func (vSockAuthenticator) IsVsock() bool {
	return true
}

// AuthenticateInstance authenticates a VM accessing /dev/lxd over vsock using its agent certificate,
// and returns the corresponding VM instance.
func (vSockAuthenticator) AuthenticateInstance(d *Daemon, r *http.Request) (instance.Instance, error) {
	trusted, inst, err := authenticateAgentCert(d.State(), r)
	if err != nil {
		return nil, api.NewStatusError(http.StatusInternalServerError, err.Error())
	}

	if !trusted {
		return nil, api.NewGenericStatusError(http.StatusUnauthorized)
	}

	return inst, nil
}

// microVMIdentityFromAddr extracts the project and instance name tagged onto addr by the libkrun
// vsock proxy (see instanceDrivers.MicroVMPeerAddrPrefix), which resolves them (via ucred +
// /proc/<pid>/cmdline) before dialing into the shared vsock-unix.socket listener on the MicroVM's
// behalf. Returns ok=false if addr isn't tagged with a MicroVM identity.
func microVMIdentityFromAddr(addr string) (projectName string, instName string, ok bool) {
	tag, found := strings.CutPrefix(addr, instanceDrivers.MicroVMPeerAddrPrefix)
	if !found {
		return "", "", false
	}

	projectName, instName, found = strings.Cut(tag, "/")
	if !found || projectName == "" || instName == "" {
		return "", "", false
	}

	return projectName, instName, true
}

func authenticateAgentCert(s *state.State, r *http.Request) (bool, instance.Instance, error) {
	var vsockID int

	if r.TLS == nil {
		return false, nil, nil
	}

	_, err := fmt.Sscanf(r.RemoteAddr, "vm(%d)", &vsockID)
	if err != nil {
		return authenticateMicroVMAgentCertFunc(s, r)
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

	return agentCertMatchesRequest(inst, r)
}

// authenticateMicroVMAgentCert authenticates a MicroVM's lxd-agent connection arriving over the
// shared vsock-unix.socket listener (used because libkrun MicroVMs have no kernel vsock CID to
// look up, unlike QEMU VMs). The libkrun vsock proxy tags its dial to that socket with the
// project/instance it resolved from the connecting libkrun helper's PID, so this verifies a single
// candidate's certificate instead of iterating every running MicroVM's, which would not scale with
// the number of MicroVMs on the host. Untagged connections are not trusted.
func authenticateMicroVMAgentCert(s *state.State, r *http.Request) (bool, instance.Instance, error) {
	projectName, instName, ok := microVMIdentityFromAddr(r.RemoteAddr)
	if ok {
		inst, err := instance.LoadByProjectAndName(s, projectName, instName)
		if err != nil {
			return false, nil, err
		}

		if inst.Type() != instancetype.MicroVM {
			return false, nil, nil
		}

		return agentCertMatchesRequest(inst, r)
	}

	return false, nil, nil
}

// agentCertMatchesRequest reports whether any of the request's TLS peer certificates matches
// inst's agent certificate.
func agentCertMatchesRequest(inst instance.Instance, r *http.Request) (bool, instance.Instance, error) {
	vm, ok := inst.(instance.VM)
	if !ok {
		return false, nil, nil
	}

	agentCert := vm.AgentCertificate()
	if agentCert == nil {
		return false, nil, nil
	}

	trustedCerts := map[string]x509.Certificate{"0": *agentCert}

	for _, cert := range r.TLS.PeerCertificates {
		trusted, _ := util.CheckMutualTLS(*cert, trustedCerts)
		if trusted {
			return true, inst, nil
		}
	}

	return false, nil, nil
}
