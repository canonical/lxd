package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
)

func certGenerateFingerprint(cert *x509.Certificate) string {
	return fmt.Sprintf("%x", sha256.Sum256(cert.Raw))
}

func certificatesGet(d *Daemon, r *http.Request) Response {
	recursion := d.isRecursionRequest(r)

	if recursion {
		certResponses := []shared.CertInfo{}

		baseCerts, err := dbCertsGet(d.db)
		if err != nil {
			return SmartError(err)
		}
		for _, baseCert := range baseCerts {
			resp := shared.CertInfo{}
			resp.Fingerprint = baseCert.Fingerprint
			resp.Certificate = baseCert.Certificate
			if baseCert.Type == 1 {
				resp.Type = "client"
			} else {
				resp.Type = "unknown"
			}
			certResponses = append(certResponses, resp)
		}
		return SyncResponse(true, certResponses)
	}

	body := []string{}
	for _, cert := range d.clientCerts {
		fingerprint := certGenerateFingerprint(&cert)
		body = append(body, fingerprint)
	}

	return SyncResponse(true, body)
}

type certificatesPostBody struct {
	Type        string `json:"type"`
	Certificate string `json:"certificate"`
	Name        string `json:"name"`
	Password    string `json:"password"`
}

func readSavedClientCAList(d *Daemon) {
	d.clientCerts = []x509.Certificate{}

	dbCerts, err := dbCertsGet(d.db)
	if err != nil {
		shared.Logf("Error reading certificates from database: %s", err)
		return
	}

	for _, dbCert := range dbCerts {
		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			shared.Logf("Error reading certificate for %s: %s", dbCert.Name, err)
			continue
		}
		d.clientCerts = append(d.clientCerts, *cert)
	}
}

func saveCert(d *Daemon, host string, cert *x509.Certificate) error {
	baseCert := new(dbCertInfo)
	baseCert.Fingerprint = certGenerateFingerprint(cert)
	baseCert.Type = 1
	baseCert.Name = host
	baseCert.Certificate = string(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
	)

	return dbCertSave(d.db, baseCert)
}

func certificatesPost(d *Daemon, r *http.Request) Response {
	req := certificatesPostBody{}

	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	if req.Type != "client" {
		return BadRequest(fmt.Errorf("Unknown request type %s", req.Type))
	}

	var cert *x509.Certificate
	var name string
	if req.Certificate != "" {

		data, err := base64.StdEncoding.DecodeString(req.Certificate)
		if err != nil {
			return BadRequest(err)
		}

		cert, err = x509.ParseCertificate(data)
		if err != nil {
			return BadRequest(err)
		}
		name = req.Name

	} else if r.TLS != nil {

		if len(r.TLS.PeerCertificates) < 1 {
			return BadRequest(fmt.Errorf("No client certificate provided"))
		}
		cert = r.TLS.PeerCertificates[len(r.TLS.PeerCertificates)-1]

		remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return InternalError(err)
		}

		name = remoteHost
	} else {
		return BadRequest(fmt.Errorf("Can't use TLS data on non-TLS link"))
	}

	fingerprint := certGenerateFingerprint(cert)
	for _, existingCert := range d.clientCerts {
		if fingerprint == certGenerateFingerprint(&existingCert) {
			return EmptySyncResponse
		}
	}

	if !d.isTrustedClient(r) && !d.PasswordCheck(req.Password) {
		return Forbidden
	}

	err := saveCert(d, name, cert)
	if err != nil {
		return SmartError(err)
	}

	d.clientCerts = append(d.clientCerts, *cert)

	return EmptySyncResponse
}

var certificatesCmd = Command{
	"certificates",
	false,
	true,
	certificatesGet,
	nil,
	certificatesPost,
	nil,
}

func certificateFingerprintGet(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	cert, err := doCertificateGet(d, fingerprint)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, cert)
}

func doCertificateGet(d *Daemon, fingerprint string) (shared.CertInfo, error) {
	resp := shared.CertInfo{}

	dbCertInfo, err := dbCertGet(d.db, fingerprint)
	if err != nil {
		return resp, err
	}

	resp.Fingerprint = dbCertInfo.Fingerprint
	resp.Certificate = dbCertInfo.Certificate
	if dbCertInfo.Type == 1 {
		resp.Type = "client"
	} else {
		resp.Type = "unknown"
	}

	return resp, nil
}

func certificateFingerprintDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	certInfo, err := dbCertGet(d.db, fingerprint)
	if err != nil {
		return NotFound
	}

	err = dbCertDelete(d.db, certInfo.Fingerprint)
	if err != nil {
		return SmartError(err)
	}
	readSavedClientCAList(d)

	return EmptySyncResponse
}

var certificateFingerprintCmd = Command{
	"certificates/{fingerprint}",
	false,
	false,
	certificateFingerprintGet,
	nil,
	nil,
	certificateFingerprintDelete,
}
