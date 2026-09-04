package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"
	"time"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/state"
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
	orig := authenticateAgentCertByPresentedCertFunc
	defer func() {
		authenticateAgentCertByPresentedCertFunc = orig
	}()

	called := false
	authenticateAgentCertByPresentedCertFunc = func(_ *state.State, _ *http.Request) (bool, instance.Instance, error) {
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
