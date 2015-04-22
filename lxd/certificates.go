package main

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
	"golang.org/x/crypto/scrypt"
)

func (d *Daemon) hasPwd() bool {
	q := "SELECT id FROM config WHERE key=\"core.trust_password\""
	id := -1
	argIn := []interface{}{}
	argOut := []interface{}{&id}
	err := shared.DbQueryRowScan(d.db, q, argIn, argOut)
	return err == nil && id != -1
}

func (d *Daemon) verifyAdminPwd(password string) bool {
	q := "SELECT value FROM config WHERE key=\"core.trust_password\""
	value := ""
	argIn := []interface{}{}
	argOut := []interface{}{&value}
	err := shared.DbQueryRowScan(d.db, q, argIn, argOut)

	if err != nil || value == "" {
		shared.Debugf("verifyAdminPwd: no password is set")
		return false
	}

	buff, err := hex.DecodeString(value)
	if err != nil {
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

func readSavedClientCAList(d *Daemon) {
	d.clientCerts = make(map[string]x509.Certificate)
	rows, err := shared.DbQuery(d.db, "SELECT fingerprint, type, name, certificate FROM certificates")
	if err != nil {
		shared.Logf("Error reading certificates from database: %s\n", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var fp string
		var t int
		var name string
		var cf []byte
		rows.Scan(&fp, &t, &name, &cf)
		cert_block, _ := pem.Decode(cf)
		cert, err := x509.ParseCertificate(cert_block.Bytes)
		if err != nil {
			shared.Logf("Error reading certificate for %s: %s\n", name, err)
			continue
		}
		d.clientCerts[name] = *cert
	}
}

func saveCert(d *Daemon, host string, cert *x509.Certificate) error {
	tx, err := shared.DbBegin(d.db)
	if err != nil {
		return err
	}
	fingerprint := shared.GenerateFingerprint(cert)
	stmt, err := tx.Prepare("INSERT INTO certificates (fingerprint,type,name,certificate) VALUES (?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(fingerprint, 1, host, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	if err != nil {
		tx.Rollback()
		return err
	}

	return shared.TxCommit(tx)
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

		if len(r.TLS.PeerCertificates) < 1 {
			return BadRequest(fmt.Errorf("No client certificate provided"))
		}
		cert = r.TLS.PeerCertificates[len(r.TLS.PeerCertificates)-1]
		name = r.TLS.ServerName
	}

	fingerprint := shared.GenerateFingerprint(cert)
	for existingName, existingCert := range d.clientCerts {
		if name == existingName {
			if fingerprint == shared.GenerateFingerprint(&existingCert) {
				return EmptySyncResponse
			}
			return Conflict
		}
	}

	if !d.isTrustedClient(r) && !d.verifyAdminPwd(req.Password) {
		return Forbidden
	}

	err := saveCert(d, name, cert)
	if err != nil {
		return SmartError(err)
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
			_, err := shared.DbExec(d.db, "DELETE FROM certificates WHERE name=?", name)
			if err != nil {
				return SmartError(err)
			}
			return EmptySyncResponse
		}
	}

	return NotFound
}

var certificateFingerprintCmd = Command{"certificates/{fingerprint}", false, false, certificateFingerprintGet, nil, nil, certificateFingerprintDelete}
