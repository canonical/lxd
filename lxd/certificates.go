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
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

var certificatesCmd = APIEndpoint{
	Path: "certificates",

	Get:  APIEndpointAction{Handler: certificatesGet, AccessHandler: AllowAuthenticated},
	Post: APIEndpointAction{Handler: certificatesPost, AllowUntrusted: true},
}

var certificateCmd = APIEndpoint{
	Path: "certificates/{fingerprint}",

	Delete: APIEndpointAction{Handler: certificateDelete},
	Get:    APIEndpointAction{Handler: certificateGet, AccessHandler: AllowAuthenticated},
	Patch:  APIEndpointAction{Handler: certificatePatch},
	Put:    APIEndpointAction{Handler: certificatePut},
}

func certificatesGet(d *Daemon, r *http.Request) Response {
	recursion := util.IsRecursionRequest(r)

	if recursion {
		certResponses := []api.Certificate{}

		baseCerts, err := d.cluster.CertificatesGet()
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
	d.clientCerts = map[string]x509.Certificate{}

	dbCerts, err := d.cluster.CertificatesGet()
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

func certificatesPost(d *Daemon, r *http.Request) Response {
	// Parse the request
	req := api.CertificatesPost{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	// Access check
	secret, err := cluster.ConfigGetString(d.cluster, "core.trust_password")
	if err != nil {
		return SmartError(err)
	}

	trusted, _, protocol, err := d.Authenticate(r)
	if err != nil {
		return SmartError(err)
	}

	if (!trusted || (protocol == "candid" && !d.userIsAdmin(r))) && util.PasswordCheck(secret, req.Password) != nil {
		if req.Password != "" {
			logger.Warn("Bad trust password", log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		}
		return Forbidden(nil)
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
			return BadRequest(errors.Wrap(err, "invalid certificate material"))
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

	if d.clientCerts == nil {
		d.clientCerts = map[string]x509.Certificate{}
	}

	if !isClusterNotification(r) {
		// Check if we already have the certificate
		existingCert, _ := d.cluster.CertificateGet(fingerprint)
		if existingCert != nil {
			// Deal with the cache being potentially out of sync
			_, ok := d.clientCerts[fingerprint]
			if !ok {
				d.clientCerts[fingerprint] = *cert
				return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", version.APIVersion, fingerprint))
			}

			return BadRequest(fmt.Errorf("Certificate already in trust store"))
		}

		// Store the certificate in the cluster database
		dbCert := db.CertInfo{
			Fingerprint: shared.CertFingerprint(cert),
			Type:        1,
			Name:        name,
			Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		}

		err = d.cluster.CertSave(&dbCert)
		if err != nil {
			return SmartError(err)
		}

		// Notify other nodes about the new certificate.
		notifier, err := cluster.NewNotifier(
			d.State(), d.endpoints.NetworkCert(), cluster.NotifyAlive)
		if err != nil {
			return SmartError(err)
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
			return SmartError(err)
		}
	}

	d.clientCerts[shared.CertFingerprint(cert)] = *cert

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", version.APIVersion, fingerprint))
}

func certificateGet(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	cert, err := doCertificateGet(d.cluster, fingerprint)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseETag(true, cert, cert)
}

func doCertificateGet(db *db.Cluster, fingerprint string) (api.Certificate, error) {
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

func certificatePut(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d.cluster, fingerprint)
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

func certificatePatch(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	oldEntry, err := doCertificateGet(d.cluster, fingerprint)
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

	err := d.cluster.CertUpdate(fingerprint, req.Name, 1)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func certificateDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	certInfo, err := d.cluster.CertificateGet(fingerprint)
	if err != nil {
		return NotFound(err)
	}

	err = d.cluster.CertDelete(certInfo.Fingerprint)
	if err != nil {
		return SmartError(err)
	}
	readSavedClientCAList(d)

	return EmptySyncResponse
}
