package request

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

func TestGetPeerCertificate(t *testing.T) {
	certA := &x509.Certificate{Raw: []byte("certA")}
	certB := &x509.Certificate{Raw: []byte("certB")}

	tests := []struct {
		name        string
		tlsState    *tls.ConnectionState
		wantCert    *x509.Certificate
		wantErr     error
		wantErrCode int
	}{
		{
			name:        "no TLS connection",
			wantErr:     ErrNoTLS,
			wantErrCode: http.StatusBadRequest,
		},
		{
			name: "TLS with empty peer certificates",
			tlsState: &tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{},
			},
			wantErr:     ErrNoCertificate,
			wantErrCode: http.StatusBadRequest,
		},
		{
			name: "TLS with single certificate",
			tlsState: &tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{certA},
			},
			wantCert: certA,
		},
		{
			name: "TLS with certificate chain",
			tlsState: &tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{certA, certB},
			},
			wantCert: certA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err)
			req.TLS = tt.tlsState

			cert, err := GetPeerCertificate(req)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.wantCert, cert)
			if err != nil && tt.wantErrCode != 0 {
				require.True(t, api.StatusErrorCheck(err, tt.wantErrCode))
			}
		})
	}
}
