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

	log "github.com/lxc/lxd/shared/log15"
)

var certificatesCmd = APIEndpoint{
	Path: "certificates",

	Get:  APIEndpointAction{Handler: certificatesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: certificatesPost, AllowUntrusted: true},
}

var certificateCmd = APIEndpoint{
	Path: "certificates/{fingerprint}",

	Delete: APIEndpointAction{Handler: certificateDelete},
	Get:    APIEndpointAction{Handler: certificateGet, AccessHandler: allowAuthenticated},
	Patch:  APIEndpointAction{Handler: certificatePatch},
	Put:    APIEndpointAction{Handler: certificatePut},
}

func certificatesGet(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)

	if recursion {
		certResponses := []api.Certificate{}

		var baseCerts []db.Certificate
		var err error
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			baseCerts, err = tx.GetCertificates(db.CertificateFilter{})
			return err
		})
		if err != nil {
			return response.SmartError(err)
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
		return response.SyncResponse(true, certResponses)
	}

	body := []string{}
	for _, cert := range d.clientCerts {
		fingerprint := fmt.Sprintf("/%s/certificates/%s", version.APIVersion, shared.CertFingerprint(&cert))
		body = append(body, fingerprint)
	}

	return response.SyncResponse(true, body)
}

func readSavedClientCAList(d *Daemon) {
	d.clientCerts = map[string]x509.Certificate{}

	var dbCerts []db.Certificate
	var err error
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		dbCerts, err = tx.GetCertificates(db.CertificateFilter{})
		return err
	})
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

		d.clientCerts[shared.CertFingerprint(cert)] = *cert
	}
}

func certificatesPost(d *Daemon, r *http.Request) response.Response {
	// Parse the request
	req := api.CertificatesPost{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return response.BadRequest(err)
	}

	// Access check
	secret, err := cluster.ConfigGetString(d.cluster, "core.trust_password")
	if err != nil {
		return response.SmartError(err)
	}

	trusted, _, protocol, err := d.Authenticate(nil, r)
	if err != nil {
		return response.SmartError(err)
	}

	if (!trusted || (protocol == "candid" && !d.userIsAdmin(r))) && util.PasswordCheck(secret, req.Password) != nil {
		if req.Password != "" {
			logger.Warn("Bad trust password", log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		}
		return response.Forbidden(nil)
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
			return response.BadRequest(errors.Wrap(err, "invalid certificate material"))
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

	if d.clientCerts == nil {
		d.clientCerts = map[string]x509.Certificate{}
	}

	if !isClusterNotification(r) {
		// Check if we already have the certificate
		existingCert, _ := d.cluster.GetCertificate(fingerprint)
		if existingCert != nil {
			// Deal with the cache being potentially out of sync
			_, ok := d.clientCerts[fingerprint]
			if !ok {
				d.clientCerts[fingerprint] = *cert
				return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", version.APIVersion, fingerprint))
			}

			return response.BadRequest(fmt.Errorf("Certificate already in trust store"))
		}

		// Store the certificate in the cluster database
		dbCert := db.Certificate{
			Fingerprint: shared.CertFingerprint(cert),
			Type:        1,
			Name:        name,
			Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		}

		err = d.cluster.CreateCertificate(dbCert)
		if err != nil {
			return response.SmartError(err)
		}

		// Notify other nodes about the new certificate.
		notifier, err := cluster.NewNotifier(
			d.State(), d.endpoints.NetworkCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}
		req := api.CertificatesPost{
			Certificate: base64.StdEncoding.EncodeToString(cert.Raw),
		}
		req.Name = name
		req.Type = "client"

		err = notifier(func(client lxd.InstanceServer) error {
			return client.CreateCertificate(req)
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	d.clientCerts[shared.CertFingerprint(cert)] = *cert

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", version.APIVersion, fingerprint))
}

func certificateGet(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	cert, err := doCertificateGet(d.cluster, fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, cert, cert)
}

func doCertificateGet(db *db.Cluster, fingerprint string) (api.Certificate, error) {
	resp := api.Certificate{}

	dbCertInfo, err := db.GetCertificate(fingerprint)
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

func certificatePut(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d.cluster, fingerprint)
	if err != nil {
		return response.SmartError(err)
	}
	fingerprint = oldEntry.Fingerprint

	err = util.EtagCheck(r, oldEntry)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.CertificatePut{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return response.BadRequest(err)
	}

	return doCertificateUpdate(d, fingerprint, req)
}

func certificatePatch(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d.cluster, fingerprint)
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

	return doCertificateUpdate(d, fingerprint, req.Writable())
}

func doCertificateUpdate(d *Daemon, fingerprint string, req api.CertificatePut) response.Response {
	if req.Type != "client" {
		return response.BadRequest(fmt.Errorf("Unknown request type %s", req.Type))
	}

	err := d.cluster.RenameCertificate(fingerprint, req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func certificateDelete(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	certInfo, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.NotFound(err)
	}

	err = d.cluster.DeleteCertificate(certInfo.Fingerprint)
	if err != nil {
		return response.SmartError(err)
	}
	readSavedClientCAList(d)

	return response.EmptySyncResponse
}
