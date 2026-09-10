package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/shared/api"
)

func TestAddressSetChanged(t *testing.T) {
	tests := []struct {
		name    string
		current []string
		updated []string
		want    bool
	}{
		{
			name:    "identical slices",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    false,
		},
		{
			name:    "same addresses different order",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.2:8443", "10.0.0.1:8443"},
			want:    false,
		},
		{
			name:    "address added",
			current: []string{"10.0.0.1:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    true,
		},
		{
			name:    "address removed",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "address replaced",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.3:8443"},
			want:    true,
		},
		{
			name:    "both empty",
			current: []string{},
			updated: []string{},
			want:    false,
		},
		{
			name:    "current empty updated non-empty",
			current: []string{},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "current non-empty updated empty",
			current: []string{"10.0.0.1:8443"},
			updated: []string{},
			want:    true,
		},
		{
			name:    "nil slices",
			current: nil,
			updated: nil,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := addressSetChanged(tc.current, tc.updated)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsUnreachableError(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	dnsErr := &net.DNSError{Err: "no such host", Name: "cluster.example.net", IsNotFound: true}
	certErr := &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "connection refused",
			err:  dialErr,
			want: true,
		},
		{
			name: "dns failure",
			err:  dnsErr,
			want: true,
		},
		{
			name: "deadline exceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "wrapped dial failure is still unreachable",
			err:  fmt.Errorf("Failed connecting to %q: %w", "10.0.0.1:8443", dialErr),
			want: true,
		},
		{
			name: "joined dial failures are still unreachable",
			err:  errors.Join(dialErr, dnsErr),
			want: true,
		},
		{
			name: "certificate verification failure means the cluster answered",
			err:  certErr,
			want: false,
		},
		{
			name: "wrapped certificate failure means the cluster answered",
			err:  fmt.Errorf("Failed connecting to %q: %w", "10.0.0.1:8443", certErr),
			want: false,
		},
		{
			name: "hostname mismatch means the cluster answered",
			err:  x509.HostnameError{Host: "10.0.0.1"},
			want: false,
		},
		{
			name: "unknown authority means the cluster answered",
			err:  x509.UnknownAuthorityError{},
			want: false,
		},
		{
			name: "expired certificate means the cluster answered",
			err:  x509.CertificateInvalidError{Reason: x509.Expired},
			want: false,
		},
		{
			name: "http rejection means the cluster answered",
			err:  api.NewStatusError(http.StatusForbidden, "not authorized"),
			want: false,
		},
		{
			name: "unrecognised errors are not treated as unreachable",
			err:  errors.New("something else went wrong"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isUnreachableError(tc.err)
			assert.Equal(t, tc.want, got)
		})
	}
}
