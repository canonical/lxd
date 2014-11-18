package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/lxc/lxd"
)

func (d *Daemon) serveTrustAdd(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to trustadd")

	password := r.FormValue("password")
	if password == "" {
		fmt.Fprintf(w, "failed parsing password")
		return
	}

	if r.TLS == nil {
		lxd.Debugf("Could not get client certificate to add to trust list")
		return
	}

	if ! d.verify_admin_password(password) {
		http.Error(w, "Bad admin password", 401)
		return
	}

	for i := range r.TLS.PeerCertificates {
		peercert := &r.TLS.PeerCertificates[i]
		fmt.Printf("PeerCertificate %d : %d\n", i, peercert)
		// TODO - do we need to sanity-check the server name to avoid arbitrary writes to fs?
		dirname := lxd.VarPath("clientcerts")
		err := os.MkdirAll(dirname, 0755)
		filename := fmt.Sprintf("%s/%s", dirname, r.TLS.ServerName)
		certout, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			lxd.Debugf("Error opening file for  client certificate: %q", err)
			continue
		}
		_, err = certout.Write(r.TLS.PeerCertificates[i].Raw)
		certout.Close()
		if err !=  nil {
			lxd.Debugf("Error writing client certificate: %q", err)
		}
		d.clientCerts[r.TLS.ServerName] = *r.TLS.PeerCertificates[i]
	}
}
