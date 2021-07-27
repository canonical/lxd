//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/lxd/lxd/db"
)

func TestGetCertificate(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	_, err := tx.CreateCertificate(db.Certificate{Fingerprint: "foobar"})
	require.NoError(t, err)

	cert, err := tx.GetCertificateByFingerprint("foo%", db.CertificateFilter{})
	require.NoError(t, err)
	assert.Equal(t, cert.Fingerprint, "foobar")
}
