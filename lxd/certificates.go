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

	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

func certificatesGet(d *Daemon, r *http.Request) response.Response {
	recursion := d.isRecursionRequest(r)

	if recursion {
		certResponses := []shared.CertInfo{}

		baseCerts, err := dbCertsGet(d.db)
		if err != nil {
			return response.SmartError(err)
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
		return response.SyncResponse(true, certResponses)
	}

	body := []string{}
	for _, cert := range d.clientCerts {
		fingerprint := fmt.Sprintf("/%s/certificates/%s", shared.APIVersion, shared.CertFingerprint(&cert))
		body = append(body, fingerprint)
	}

	return response.SyncResponse(true, body)
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
		shared.LogInfof("Error reading certificates from database: %s", err)
		return
	}

	for _, dbCert := range dbCerts {
		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			shared.LogInfof("Error decoding certificate for %s: %s", dbCert.Name, err)
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			shared.LogInfof("Error reading certificate for %s: %s", dbCert.Name, err)
			continue
		}
		d.clientCerts = append(d.clientCerts, *cert)
	}
}

func saveCert(d *Daemon, host string, cert *x509.Certificate) error {
	baseCert := new(dbCertInfo)
	baseCert.Fingerprint = shared.CertFingerprint(cert)
	baseCert.Type = 1
	baseCert.Name = host
	baseCert.Certificate = string(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
	)

	return dbCertSave(d.db, baseCert)
}

func certificatesPost(d *Daemon, r *http.Request) response.Response {
	// Parse the request
	req := certificatesPostBody{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return response.BadRequest(err)
	}

	// Access check
	if !d.isTrustedClient(r) && d.PasswordCheck(req.Password) != nil {
		return response.Forbidden
	}

	if req.Type != "client" {
		return response.BadRequest(fmt.Errorf("Unknown request type %s", req.Type))
	}

	// Extract the certificate
	var cert *x509.Certificate
	var name string
	if req.Certificate != "" {
		data, err := base64.StdEncoding.DecodeString(req.Certificate)
		if err != nil {
			return response.BadRequest(err)
		}

		cert, err = x509.ParseCertificate(data)
		if err != nil {
			return response.BadRequest(err)
		}
		name = req.Name
	} else if r.TLS != nil {
		if len(r.TLS.PeerCertificates) < 1 {
			return response.BadRequest(fmt.Errorf("No client certificate provided"))
		}
		cert = r.TLS.PeerCertificates[len(r.TLS.PeerCertificates)-1]

		remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return response.InternalError(err)
		}

		name = remoteHost
	} else {
		return response.BadRequest(fmt.Errorf("Can't use TLS data on non-TLS link"))
	}

	fingerprint := shared.CertFingerprint(cert)
	for _, existingCert := range d.clientCerts {
		if fingerprint == shared.CertFingerprint(&existingCert) {
			return response.BadRequest(fmt.Errorf("Certificate already in trust store"))
		}
	}

	err := saveCert(d, name, cert)
	if err != nil {
		return response.SmartError(err)
	}

	d.clientCerts = append(d.clientCerts, *cert)

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", shared.APIVersion, fingerprint))
}

var certificatesCmd = Command{name: "certificates", untrustedPost: true, get: certificatesGet, post: certificatesPost}

func certificateFingerprintGet(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	cert, err := doCertificateGet(d, fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, cert, cert)
}

func doCertificateGet(d *Daemon, fingerprint string) (shared.CertInfo, error) {
	resp := shared.CertInfo{}

	dbCertInfo, err := dbCertGet(d.db, fingerprint)
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

func certificateFingerprintPut(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d, fingerprint)
	if err != nil {
		return response.SmartError(err)
	}
	fingerprint = oldEntry.Fingerprint

	err = util.EtagCheck(r, oldEntry)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := shared.CertInfo{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return response.BadRequest(err)
	}

	return doCertificateUpdate(d, fingerprint, req)
}

func certificateFingerprintPatch(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d, fingerprint)
	if err != nil {
		return response.SmartError(err)
	}
	fingerprint = oldEntry.Fingerprint

	err = util.EtagCheck(r, oldEntry)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := oldEntry
	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&reqRaw); err != nil {
		return response.BadRequest(err)
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

	return doCertificateUpdate(d, fingerprint, req)
}

func doCertificateUpdate(d *Daemon, fingerprint string, req shared.CertInfo) response.Response {
	if req.Type != "client" {
		return response.BadRequest(fmt.Errorf("Unknown request type %s", req.Type))
	}

	err := dbCertUpdate(d.db, fingerprint, req.Name, 1)
	if err != nil {
		return response.InternalError(err)
	}

	return response.EmptySyncResponse
}

func certificateFingerprintDelete(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	certInfo, err := dbCertGet(d.db, fingerprint)
	if err != nil {
		return response.NotFound
	}

	err = dbCertDelete(d.db, certInfo.Fingerprint)
	if err != nil {
		return response.SmartError(err)
	}
	readSavedClientCAList(d)

	return response.EmptySyncResponse
}

var certificateFingerprintCmd = Command{name: "certificates/{fingerprint}", get: certificateFingerprintGet, delete: certificateFingerprintDelete, put: certificateFingerprintPut, patch: certificateFingerprintPatch}
