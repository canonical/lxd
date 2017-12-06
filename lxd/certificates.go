package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

func certificatesGet(d *Daemon, r *http.Request) Response {
	recursion := util.IsRecursionRequest(r)

	if recursion {
		certResponses := []api.Certificate{}

		baseCerts, err := d.db.CertificatesGet()
		if err != nil {
			return SmartError(err)
		}
		for _, baseCert := range baseCerts {
			resp := api.Certificate{}
			resp.Fingerprint = baseCert.Fingerprint
			resp.Certificate = baseCert.Certificate
			resp.Name = baseCert.Name
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
		fingerprint := fmt.Sprintf("/%s/certificates/%s", version.APIVersion, shared.CertFingerprint(&cert))
		body = append(body, fingerprint)
	}

	return SyncResponse(true, body)
}

func readSavedClientCAList(d *Daemon) {
	d.clientCerts = []x509.Certificate{}

	dbCerts, err := d.db.CertificatesGet()
	if err != nil {
		logger.Infof("Error reading certificates from database: %s", err)
		return
	}

	for _, dbCert := range dbCerts {
		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			logger.Infof("Error decoding certificate for %s: %s", dbCert.Name, err)
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Infof("Error reading certificate for %s: %s", dbCert.Name, err)
			continue
		}
		d.clientCerts = append(d.clientCerts, *cert)
	}
}

func saveCert(dbObj *db.Node, host string, cert *x509.Certificate) error {
	baseCert := new(db.CertInfo)
	baseCert.Fingerprint = shared.CertFingerprint(cert)
	baseCert.Type = 1
	baseCert.Name = host
	baseCert.Certificate = string(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
	)

	return dbObj.CertSave(baseCert)
}

func certificatesPost(d *Daemon, r *http.Request) Response {
	// Parse the request
	req := api.CertificatesPost{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	// Access check
	secret := daemonConfig["core.trust_password"].Get()
	if d.checkTrustedClient(r) != nil && util.PasswordCheck(secret, req.Password) != nil {
		return Forbidden
	}

	if req.Type != "client" {
		return BadRequest(fmt.Errorf("Unknown request type %s", req.Type))
	}

	// Extract the certificate
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

	fingerprint := shared.CertFingerprint(cert)
	for _, existingCert := range d.clientCerts {
		if fingerprint == shared.CertFingerprint(&existingCert) {
			return BadRequest(fmt.Errorf("Certificate already in trust store"))
		}
	}

	err := saveCert(d.db, name, cert)
	if err != nil {
		return SmartError(err)
	}

	d.clientCerts = append(d.clientCerts, *cert)

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", version.APIVersion, fingerprint))
}

var certificatesCmd = Command{name: "certificates", untrustedPost: true, get: certificatesGet, post: certificatesPost}

func certificateFingerprintGet(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	cert, err := doCertificateGet(d.db, fingerprint)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseETag(true, cert, cert)
}

func doCertificateGet(db *db.Node, fingerprint string) (api.Certificate, error) {
	resp := api.Certificate{}

	dbCertInfo, err := db.CertificateGet(fingerprint)
	if err != nil {
		return resp, err
	}

	resp.Fingerprint = dbCertInfo.Fingerprint
	resp.Certificate = dbCertInfo.Certificate
	resp.Name = dbCertInfo.Name
	if dbCertInfo.Type == 1 {
		resp.Type = "client"
	} else {
		resp.Type = "unknown"
	}

	return resp, nil
}

func certificateFingerprintPut(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d.db, fingerprint)
	if err != nil {
		return SmartError(err)
	}
	fingerprint = oldEntry.Fingerprint

	err = util.EtagCheck(r, oldEntry)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.CertificatePut{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	return doCertificateUpdate(d, fingerprint, req)
}

func certificateFingerprintPatch(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d.db, fingerprint)
	if err != nil {
		return SmartError(err)
	}
	fingerprint = oldEntry.Fingerprint

	err = util.EtagCheck(r, oldEntry)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := oldEntry
	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&reqRaw); err != nil {
		return BadRequest(err)
	}

	// Get name
	value, err := reqRaw.GetString("name")
	if err == nil {
		req.Name = value
	}

	// Get type
	value, err = reqRaw.GetString("type")
	if err == nil {
		req.Type = value
	}

	return doCertificateUpdate(d, fingerprint, req.Writable())
}

func doCertificateUpdate(d *Daemon, fingerprint string, req api.CertificatePut) Response {
	if req.Type != "client" {
		return BadRequest(fmt.Errorf("Unknown request type %s", req.Type))
	}

	err := d.db.CertUpdate(fingerprint, req.Name, 1)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func certificateFingerprintDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	certInfo, err := d.db.CertificateGet(fingerprint)
	if err != nil {
		return NotFound
	}

	err = d.db.CertDelete(certInfo.Fingerprint)
	if err != nil {
		return SmartError(err)
	}
	readSavedClientCAList(d)

	return EmptySyncResponse
}

var certificateFingerprintCmd = Command{name: "certificates/{fingerprint}", get: certificateFingerprintGet, delete: certificateFingerprintDelete, put: certificateFingerprintPut, patch: certificateFingerprintPatch}
