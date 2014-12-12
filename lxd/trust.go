package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"

	"github.com/lxc/lxd"
	"golang.org/x/crypto/scrypt"
)

func (d *Daemon) hasPwd() bool {
	passfname := lxd.VarPath("adminpwd")
	_, err := os.Open(passfname)
	return err == nil
}

func (d *Daemon) verifyAdminPwd(password string) bool {
	passfname := lxd.VarPath("adminpwd")
	passOut, err := os.Open(passfname)
	if err != nil {
		lxd.Debugf("verifyAdminPwd: no password is set")
		return false
	}
	defer passOut.Close()
	buff := make([]byte, PW_SALT_BYTES+PW_HASH_BYTES)
	_, err = passOut.Read(buff)
	if err != nil {
		lxd.Debugf("failed to read the saved admin pasword for verification")
		return false
	}
	salt := buff[0:PW_SALT_BYTES]
	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
	if err != nil {
		lxd.Debugf("failed to create hash to check")
		return false
	}
	if !bytes.Equal(hash, buff[PW_SALT_BYTES:]) {
		lxd.Debugf("Bad password received")
		return false
	}
	lxd.Debugf("Verified the admin password")
	return true
}

func trustGet(d *Daemon, w http.ResponseWriter, r *http.Request) {
	body := make([]lxd.Jmap, 0)
	for host, cert := range d.clientCerts {
		fingerprint := fmt.Sprintf("%:x", sha256.Sum256(cert.Raw))
		body = append(body, lxd.Jmap{"host": host, "fingerprint": fingerprint})
	}

	SyncResponse(true, body, w)
}

type trustPostBody struct {
	Type        string `json:"type"`
	Certificate string `json:"certificate"`
	Password    string `json:"password"`
}

func saveCert(host string, cert *x509.Certificate) error {
	// TODO - do we need to sanity-check the server name to avoid arbitrary writes to fs?
	dirname := lxd.VarPath("clientcerts")
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

func trustPost(d *Daemon, w http.ResponseWriter, r *http.Request) {
	req := trustPostBody{}

	if err := lxd.ReadToJson(r.Body, &req); err != nil {
		BadRequest(w, err)
		return
	}

	var cert *x509.Certificate
	if req.Certificate != "" {

		data, err := base64.StdEncoding.DecodeString(req.Certificate)
		if err != nil {
			BadRequest(w, err)
			return
		}

		cert, err = x509.ParseCertificate(data)
		if err != nil {
			BadRequest(w, err)
		}

	} else {
		cert = r.TLS.PeerCertificates[len(r.TLS.PeerCertificates)-1]
	}

	err := saveCert(r.TLS.ServerName, cert)
	if err != nil {
		InternalError(w, err)
		return
	}

	d.clientCerts[r.TLS.ServerName] = *cert

	EmptySyncResponse(w)
}

var trustCmd = Command{"trust", false, true, trustGet, nil, trustPost, nil}
