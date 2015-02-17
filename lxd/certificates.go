package main

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
	"golang.org/x/crypto/scrypt"
)

func (d *Daemon) hasPwd() bool {
	passfname := shared.VarPath("adminpwd")
	_, err := os.Open(passfname)
	return err == nil
}

func (d *Daemon) verifyAdminPwd(password string) bool {
	passfname := shared.VarPath("adminpwd")
	passOut, err := os.Open(passfname)
	if err != nil {
		shared.Debugf("verifyAdminPwd: no password is set")
		return false
	}
	defer passOut.Close()
	buff := make([]byte, PW_SALT_BYTES+PW_HASH_BYTES)
	_, err = passOut.Read(buff)
	if err != nil {
		shared.Debugf("failed to read the saved admin pasword for verification")
		return false
	}
	salt := buff[0:PW_SALT_BYTES]
	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
	if err != nil {
		shared.Debugf("failed to create hash to check")
		return false
	}
	if !bytes.Equal(hash, buff[PW_SALT_BYTES:]) {
		shared.Debugf("Bad password received")
		return false
	}
	shared.Debugf("Verified the admin password")
	return true
}

func certificatesGet(d *Daemon, r *http.Request) Response {
	body := shared.Jmap{}
	for host, cert := range d.clientCerts {
		fingerprint := shared.GenerateFingerprint(&cert)
		body[host] = fingerprint
	}

	return SyncResponse(true, body)
}

type certificatesPostBody struct {
	Type        string `json:"type"`
	Certificate string `json:"certificate"`
	Name        string `json:"name"`
	Password    string `json:"password"`
}

func saveCert(host string, cert *x509.Certificate) error {
	// TODO - do we need to sanity-check the server name to avoid arbitrary writes to fs?
	dirname := shared.VarPath("clientcerts")
	err := os.MkdirAll(dirname, 0755)
	filename := fmt.Sprintf("%s/%s.crt", dirname, host)
	certOut, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer certOut.Close()

	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err != nil {
		return err
	}

	return nil
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

	} else {
		cert = r.TLS.PeerCertificates[len(r.TLS.PeerCertificates)-1]
		name = r.TLS.ServerName
	}

	fingerprint := shared.GenerateFingerprint(cert)
	for existingName, existingCert := range d.clientCerts {
		if name == existingName {
			if fingerprint == shared.GenerateFingerprint(&existingCert) {
				return EmptySyncResponse
			} else {
				return Conflict
			}
		}
	}

	if !d.isTrustedClient(r) && !d.verifyAdminPwd(req.Password) {
		return Forbidden
	}

	err := saveCert(name, cert)
	if err != nil {
		return InternalError(err)
	}

	d.clientCerts[name] = *cert

	return EmptySyncResponse
}

var certificatesCmd = Command{"certificates", false, true, certificatesGet, nil, certificatesPost, nil}

func certificateFingerprintGet(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	for _, cert := range d.clientCerts {
		if fingerprint == shared.GenerateFingerprint(&cert) {
			b64 := base64.StdEncoding.EncodeToString(cert.Raw)
			body := shared.Jmap{"type": "client", "certificates": b64}
			return SyncResponse(true, body)
		}
	}

	return NotFound
}

func certificateFingerprintDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]
	for name, cert := range d.clientCerts {
		if fingerprint == shared.GenerateFingerprint(&cert) {
			delete(d.clientCerts, name)
			fpath := path.Join(shared.VarPath("clientcerts"), fmt.Sprintf("%s.crt", name))
			err := os.Remove(fpath)
			if err != nil {
				return SmartError(err)
			} else {
				return EmptySyncResponse
			}
		}
	}

	return NotFound
}

var certificateFingerprintCmd = Command{"certificates/{fingerprint}", false, false, certificateFingerprintGet, nil, nil, certificateFingerprintDelete}
