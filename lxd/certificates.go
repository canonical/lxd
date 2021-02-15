package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

type certificateCache struct {
	Certificates map[string]x509.Certificate
	Projects     map[string][]string
	Lock         sync.Mutex
}

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
			certResponses = append(certResponses, baseCert.ToAPI())
		}
		return response.SyncResponse(true, certResponses)
	}

	body := []string{}

	d.clientCerts.Lock.Lock()
	cache := d.clientCerts
	d.clientCerts.Lock.Unlock()

	for _, cert := range cache.Certificates {
		fingerprint := fmt.Sprintf("/%s/certificates/%s", version.APIVersion, shared.CertFingerprint(&cert))
		body = append(body, fingerprint)
	}

	return response.SyncResponse(true, body)
}

func updateCertificateCache(d *Daemon) {
	newCerts := map[string]x509.Certificate{}
	newProjects := map[string][]string{}

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

		newCerts[shared.CertFingerprint(cert)] = *cert
		if dbCert.Restricted {
			newProjects[shared.CertFingerprint(cert)] = dbCert.Projects
		}
	}

	d.clientCerts.Lock.Lock()
	d.clientCerts.Certificates = newCerts
	d.clientCerts.Projects = newProjects
	d.clientCerts.Lock.Unlock()
}

func certificatesPost(d *Daemon, r *http.Request) response.Response {
	// Parse the request
	req := api.CertificatesPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
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

	if (!trusted || (protocol == "candid" && !rbac.UserIsAdmin(r))) && util.PasswordCheck(secret, req.Password) != nil {
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
		// Add supplied certificate.
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
		// Add client's certificate.
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

	if !isClusterNotification(r) {
		// Check if we already have the certificate
		existingCert, _ := d.cluster.GetCertificate(fingerprint)
		if existingCert != nil {
			return response.BadRequest(fmt.Errorf("Certificate already in trust store"))
		}

		// Store the certificate in the cluster database
		dbCert := db.Certificate{
			Fingerprint: shared.CertFingerprint(cert),
			Type:        1,
			Name:        name,
			Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
			Restricted:  req.Restricted,
			Projects:    req.Projects,
		}

		id, err := d.cluster.CreateCertificate(dbCert)
		if err != nil {
			return response.SmartError(err)
		}

		err = d.cluster.UpdateCertificateProjects(int(id), dbCert.Projects)
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

	// Reload the cache.
	updateCertificateCache(d)

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", version.APIVersion, fingerprint))
}

func certificateGet(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	dbCertInfo, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	cert := dbCertInfo.ToAPI()
	return response.SyncResponseETag(true, cert, cert)
}

func certificatePut(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	// Get current database record.
	oldEntry, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	// Expand the fingerprint.
	fingerprint = oldEntry.Fingerprint

	// Validate the ETag.
	err = util.EtagCheck(r, oldEntry.ToAPI())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request.
	req := api.CertificatePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Apply the update.
	return doCertificateUpdate(d, *oldEntry, fingerprint, req)
}

func certificatePatch(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	// Get current database record.
	oldEntry, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	// Expand the fingerprint.
	fingerprint = oldEntry.Fingerprint

	// Validate the ETag.
	err = util.EtagCheck(r, oldEntry.ToAPI())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Apply the changes.
	req := oldEntry.ToAPI()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	return doCertificateUpdate(d, *oldEntry, fingerprint, req.Writable())
}

func doCertificateUpdate(d *Daemon, dbInfo db.Certificate, fingerprint string, req api.CertificatePut) response.Response {
	// We only support client certificates for now.
	if req.Type != "client" {
		return response.BadRequest(fmt.Errorf("Unknown request type %s", req.Type))
	}

	// Convert to the database type.
	cert := db.Certificate{
		// Read-only fields.
		Certificate: dbInfo.Certificate,
		Fingerprint: dbInfo.Fingerprint,
		Type:        dbInfo.Type,

		Restricted: req.Restricted,
		Projects:   req.Projects,
		Name:       req.Name,
	}

	// Update the database record.
	err := d.cluster.UpdateCertificate(fingerprint, cert)
	if err != nil {
		return response.SmartError(err)
	}

	err = d.cluster.UpdateCertificateProjects(dbInfo.ID, cert.Projects)
	if err != nil {
		return response.SmartError(err)
	}

	// Reload the cache.
	updateCertificateCache(d)

	return response.EmptySyncResponse
}

func certificateDelete(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	// Get current database record.
	certInfo, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.NotFound(err)
	}

	// Perform the delete with the expanded fingerprint.
	err = d.cluster.DeleteCertificate(certInfo.Fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	// Reload the cache.
	updateCertificateCache(d)

	return response.EmptySyncResponse
}
