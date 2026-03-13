package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"sync"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/registration"
	"github.com/stretchr/testify/require"
)

func Test_certificateNeedsUpdate(t *testing.T) {
	type args struct {
		domain string
		cert   *x509.Certificate
	}

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"Certificate is valid for more than 30 days",
			args{
				domain: "lxd.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"lxd.example.net"},
					NotAfter: time.Now().Add(90 * 24 * time.Hour),
				},
			},
			false,
		},
		{
			"Certificate is valid for less than 30 days",
			args{
				domain: "lxd.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"lxd.example.net"},
					NotAfter: time.Now().Add(15 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Domain differs from certificate and is valid for more than 30 days",
			args{
				domain: "lxd.example.org",
				cert: &x509.Certificate{
					DNSNames: []string{"lxd.example.net"},
					NotAfter: time.Now().Add(90 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Domain differs from certificate and is valid for less than 30 days",
			args{
				domain: "lxd.example.org",
				cert: &x509.Certificate{
					DNSNames: []string{"lxd.example.net"},
					NotAfter: time.Now().Add(15 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Domain matches one of multiple DNS names",
			args{
				domain: "lxd.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"other.example.net", "lxd.example.net", "extra.example.net"},
					NotAfter: time.Now().Add(90 * 24 * time.Hour),
				},
			},
			false,
		},
		{
			"Certificate has no DNS names",
			args{
				domain: "lxd.example.net",
				cert: &x509.Certificate{
					DNSNames: nil,
					NotAfter: time.Now().Add(90 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Certificate expires exactly at the 30 day boundary",
			args{
				domain: "lxd.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"lxd.example.net"},
					NotAfter: time.Now().Add(30 * 24 * time.Hour),
				},
			},
			true,
		},
		{
			"Certificate expires just past the 30 day boundary",
			args{
				domain: "lxd.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"lxd.example.net"},
					NotAfter: time.Now().Add(30*24*time.Hour + time.Hour),
				},
			},
			false,
		},
		{
			"Certificate already expired",
			args{
				domain: "lxd.example.net",
				cert: &x509.Certificate{
					DNSNames: []string{"lxd.example.net"},
					NotAfter: time.Now().Add(-24 * time.Hour),
				},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsUpdate := certificateNeedsUpdate(tt.args.domain, tt.args.cert)
			require.Equal(t, tt.want, needsUpdate)
		})
	}
}

func Test_user(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	reg := &registration.Resource{
		URI: "https://acme.example.com/reg/1",
	}

	u := user{
		Email:        "test@example.com",
		Registration: reg,
		Key:          privateKey,
	}

	t.Run("GetEmail", func(t *testing.T) {
		require.Equal(t, "test@example.com", u.GetEmail())
	})

	t.Run("GetRegistration", func(t *testing.T) {
		require.Equal(t, reg, u.GetRegistration())
	})

	t.Run("GetPrivateKey", func(t *testing.T) {
		require.Equal(t, privateKey, u.GetPrivateKey())
	})

	t.Run("Zero value fields", func(t *testing.T) {
		empty := user{}
		require.Empty(t, empty.GetEmail())
		require.Nil(t, empty.GetRegistration())
		require.Nil(t, empty.GetPrivateKey())
	})
}

func TestHTTP01Provider(t *testing.T) {
	t.Run("NewHTTP01Provider returns non-nil provider", func(t *testing.T) {
		p := NewHTTP01Provider()
		require.NotNil(t, p)
	})

	t.Run("Initial values are empty", func(t *testing.T) {
		p := NewHTTP01Provider()
		require.Empty(t, p.Domain())
		require.Empty(t, p.Token())
		require.Empty(t, p.KeyAuth())
	})

	t.Run("Present sets values", func(t *testing.T) {
		p := NewHTTP01Provider()

		err := p.Present("lxd.example.net", "test-token", "test-key-auth")
		require.NoError(t, err)

		require.Equal(t, "lxd.example.net", p.Domain())
		require.Equal(t, "test-token", p.Token())
		require.Equal(t, "test-key-auth", p.KeyAuth())
	})

	t.Run("CleanUp clears values", func(t *testing.T) {
		p := NewHTTP01Provider()

		err := p.Present("lxd.example.net", "test-token", "test-key-auth")
		require.NoError(t, err)

		err = p.CleanUp("lxd.example.net", "test-token", "test-key-auth")
		require.NoError(t, err)

		require.Empty(t, p.Domain())
		require.Empty(t, p.Token())
		require.Empty(t, p.KeyAuth())
	})

	t.Run("Present overwrites previous values", func(t *testing.T) {
		p := NewHTTP01Provider()

		err := p.Present("first.example.net", "token-1", "key-auth-1")
		require.NoError(t, err)

		err = p.Present("second.example.net", "token-2", "key-auth-2")
		require.NoError(t, err)

		require.Equal(t, "second.example.net", p.Domain())
		require.Equal(t, "token-2", p.Token())
		require.Equal(t, "key-auth-2", p.KeyAuth())
	})

	t.Run("Concurrent access is safe", func(t *testing.T) {
		p := NewHTTP01Provider()

		var wg sync.WaitGroup

		for i := range 50 {
			wg.Add(1)

			go func(i int) {
				defer wg.Done()

				_ = p.Present("domain.example.net", "token", "keyauth")
				_ = p.Domain()
				_ = p.Token()
				_ = p.KeyAuth()
				_ = p.CleanUp("domain.example.net", "token", "keyauth")
			}(i)
		}

		wg.Wait()
	})
}
