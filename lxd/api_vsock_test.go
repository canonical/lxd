package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

func TestAuthenticateAgentCert_NoTLS(t *testing.T) {
	trusted, inst, err := authenticateAgentCert(nil, &http.Request{RemoteAddr: "unix"})
	if err != nil {
		t.Fatalf("Expected nil error, got %v", err)
	}

	if trusted {
		t.Fatalf("Expected untrusted request when TLS is missing")
	}

	if inst != nil {
		t.Fatalf("Expected nil instance when TLS is missing")
	}
}

func TestAuthenticateAgentCert_FallbackOnNonVsockRemoteAddr(t *testing.T) {
	orig := authenticateMicroVMAgentCertFunc
	defer func() {
		authenticateMicroVMAgentCertFunc = orig
	}()

	called := false
	authenticateMicroVMAgentCertFunc = func(_ *state.State, _ *http.Request) (bool, instance.Instance, error) {
		called = true
		return true, nil, nil
	}

	req := &http.Request{
		RemoteAddr: "unix",
		TLS: &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{
				Raw:       []byte("peer-cert"),
				NotBefore: time.Now().Add(-time.Hour),
				NotAfter:  time.Now().Add(time.Hour),
			}},
		},
	}

	trusted, inst, err := authenticateAgentCert(nil, req)
	if err != nil {
		t.Fatalf("Expected nil error, got %v", err)
	}

	if !called {
		t.Fatalf("Expected fallback authenticator to be called")
	}

	if !trusted {
		t.Fatalf("Expected request to be trusted from fallback")
	}

	if inst != nil {
		t.Fatalf("Expected nil instance from fallback stub")
	}
}

// fakeAgentVM is an instance.VM that only provides an agent certificate.
type fakeAgentVM struct {
	instance.VM

	cert *x509.Certificate
}

// AgentCertificate returns the configured agent certificate.
func (f fakeAgentVM) AgentCertificate() *x509.Certificate {
	return f.cert
}

// fakeContainer is an instance.Instance that is not an instance.VM.
type fakeContainer struct {
	instance.Instance
}

// generateTestCert returns a freshly generated, currently valid certificate.
func generateTestCert(t *testing.T) *x509.Certificate {
	t.Helper()

	certPEM, _, err := shared.GenerateMemCert(false, shared.CertOptions{})
	require.NoError(t, err)

	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	return cert
}

func TestAgentCertMatchesRequest(t *testing.T) {
	agentCert := generateTestCert(t)
	otherCert := generateTestCert(t)

	request := func(certs ...*x509.Certificate) *http.Request {
		return &http.Request{TLS: &tls.ConnectionState{PeerCertificates: certs}}
	}

	tests := []struct {
		name        string
		inst        instance.Instance
		req         *http.Request
		wantTrusted bool
	}{
		{name: "matching certificate", inst: fakeAgentVM{cert: agentCert}, req: request(agentCert), wantTrusted: true},
		{name: "matching certificate among others", inst: fakeAgentVM{cert: agentCert}, req: request(otherCert, agentCert), wantTrusted: true},
		{name: "certificate of another instance", inst: fakeAgentVM{cert: agentCert}, req: request(otherCert)},
		{name: "no peer certificate", inst: fakeAgentVM{cert: agentCert}, req: request()},
		{name: "instance without agent certificate", inst: fakeAgentVM{}, req: request(agentCert)},
		{name: "instance that is not a VM", inst: fakeContainer{}, req: request(agentCert)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trusted, inst, err := agentCertMatchesRequest(tt.inst, tt.req)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTrusted, trusted)

			if tt.wantTrusted {
				assert.Equal(t, tt.inst, inst)
			} else {
				assert.Nil(t, inst)
			}
		})
	}
}

func TestAuthenticateMicroVMAgentCert_Untrusted(t *testing.T) {
	// None of these addresses resolves to a running MicroVM helper, so no instance is loaded.
	tests := []struct {
		name       string
		remoteAddr string
	}{
		{name: "not a MicroVM address", remoteAddr: "@"},
		{name: "QEMU address", remoteAddr: "vm(42):1024"},
		{name: "PID not a number", remoteAddr: "microvm(abc)"},
		{name: "PID of no process", remoteAddr: "microvm(-1)"},
		{name: "PID of a process that is not a helper", remoteAddr: "microvm(" + strconv.Itoa(os.Getpid()) + ")"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				RemoteAddr: tt.remoteAddr,
				TLS:        &tls.ConnectionState{PeerCertificates: []*x509.Certificate{generateTestCert(t)}},
			}

			trusted, inst, err := authenticateMicroVMAgentCert(nil, req)
			require.NoError(t, err)
			assert.False(t, trusted)
			assert.Nil(t, inst)
		})
	}
}
