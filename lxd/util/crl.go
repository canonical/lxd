// Provides functionality for checking the validity of
// a cert, if it's revoked
// Referenced https://github.com/shawnhankim/cori-sample/blob/2ad89204b81b30e56a91fbfa9e7908522e081a91/go/cert/x.509_validation/07_cert_revokation/revoke.go

package revoke

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	neturl "net/url"
	"sync"
	"time"

  "github.com/gorilla/mux"
  "github.com/pkg/errors"

  lxd "github.com/lxc/lxd/client"
  "github.com/lxc/lxd/lxd/cluster"
  "github.com/lxc/lxd/lxd/db"
  "github.com/lxc/lxd/lxd/response"
  "github.com/lxc/lxd/lxd/util"
  "github.com/lxc/lxd/shared"
  "github.com/lxc/lxd/shared/api"
  "github.com/lxc/lxd/shared/logger"
  "github.com/lxc/lxd/shared/version"
  "github.com/lxc/lxd/vsock/certificates"
)

var HardFail = false

var CRLSet = map[string]*pkix.CertificateList{}
var crlLock = new(sync.Mutex)

func revCheck(cert *x509.Certificate) (revoked, ok bool, err error) {
	for _, url := range cert.CRLDistributionPoints {
		if revoked, ok, err := certIsRevokedCRL(cert, url); !ok {
			errors.Wrap(err, "error checking revocation via CRL")
			if HardFail {
				return true, false, err
			}
			return false, false, err
		} else if revoked {
			errors.Wrap(err, "certificate is revoked via CRL")
			return true, true, err
		}
	}

	return false, true, nil
}

// fetchCRL fetches and parses a CRL.
func fetchCRL(url string) (*pkix.CertificateList, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	} else if resp.StatusCode >= 300 {
		return nil, errors.New("failed to retrieve CRL")
	}

	body, err := crlRead(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	return x509.ParseCRL(body)
}

func getIssuer(cert *x509.Certificate) *x509.Certificate {
	var issuer *x509.Certificate
	var err error
	for _, issuingCert := range cert.IssuingCertificateURL {
		issuer, err = fetchRemote(issuingCert)
		if err != nil {
			continue
		}
		break
	}

	return issuer

}

func certIsRevokedCRL(cert *x509.Certificate, url string) (revoked, ok bool, err error) {
	crl, ok := CRLSet[url]
	if ok && crl == nil {
		ok = false
		crlLock.Lock()
		delete(CRLSet, url)
		crlLock.Unlock()
	}

	var shouldFetchCRL = true
	if ok {
		if !crl.HasExpired(time.Now()) {
			shouldFetchCRL = false
		}
	}

	issuer := getIssuer(cert)

	if shouldFetchCRL {
		var err error
		crl, err = fetchCRL(url)
		if err != nil {
			errors.Wrap(err, failed to fetch CRL: %v", err)
			return false, false, err
		}

		// check CRL signature
		if issuer != nil {
			err = issuer.CheckCRLSignature(crl)
			if err != nil {
				errors.Wrap(err, "failed to verify CRL: %v", err)
				return false, false, err
			}
		}

		crlLock.Lock()
		CRLSet[url] = crl
		crlLock.Unlock()
	}

	for _, revoked := range crl.TBSCertList.RevokedCertificates {
		if cert.SerialNumber.Cmp(revoked.SerialNumber) == 0 {
				return true, true, err
		}
	}

	return false, true, err
}

func VerifyCertificate(cert *x509.Certificate) (revoked, ok bool) {
	revoked, ok, _ = VerifyCertificateError(cert)
	return revoked, ok
}

func VerifyCertificateError(cert *x509.Certificate) (revoked, ok bool, err error) {
	if !time.Now().Before(cert.NotAfter) {
		msg := fmt.Sprintf("Certificate expired %s\n", cert.NotAfter)
		log.Info(msg)
		return true, true, fmt.Errorf(msg)
	} else if !time.Now().After(cert.NotBefore) {
		msg := fmt.Sprintf("Certificate isn't valid until %s\n", cert.NotBefore)
		log.Info(msg)
		return true, true, fmt.Errorf(msg)
	}
	return revCheck(cert)
}

func fetchRemote(url string) (*x509.Certificate, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}

	in, err := remoteRead(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	p, _ := pem.Decode(in)
	if p != nil {
		return helpers.ParseCertificatePEM(in)
	}

	return x509.ParseCertificate(in)
}
