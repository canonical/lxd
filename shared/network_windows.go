// +build windows

package shared

import (
	"crypto/x509"
	"fmt"

	"code.cloudfoundry.org/systemcerts"
)

func systemCertPool() (*x509.CertPool, error) {
	pool := systemcerts.SystemRootsPool()
	if pool == nil {
		return nil, fmt.Errorf("Bad system root pool")
	}

	return pool.AsX509CertPool(), nil
}
