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
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	clusterRequest "github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

type certificateCache struct {
	Certificates map[db.CertificateType]map[string]x509.Certificate
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

// swagger:operation GET /1.0/certificates certificates certificates_get
//
// Get the trusted certificates
//
// Returns a list of trusted certificates (URLs).
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/certificates/390fdd27ed5dc2408edc11fe602eafceb6c025ddbad9341dfdcb1056a8dd98b1",
//               "/1.0/certificates/22aee3f051f96abe6d7756892eecabf4b4b22e2ba877840a4ca981e9ea54030a"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/certificates?recursion=1 certificates certificates_get_recursion1
//
// Get the trusted certificates
//
// Returns a list of trusted certificates (structs).
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of certificates
//           items:
//             $ref: "#/definitions/Certificate"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
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

	trustedCertificates := d.getTrustedCertificates()
	for _, certs := range trustedCertificates {
		for _, cert := range certs {
			fingerprint := fmt.Sprintf("/%s/certificates/%s", version.APIVersion, shared.CertFingerprint(&cert))
			body = append(body, fingerprint)
		}
	}

	return response.SyncResponse(true, body)
}

func updateCertificateCache(d *Daemon) {
	logger.Debug("Refreshing trusted certificate cache")

	newCerts := map[db.CertificateType]map[string]x509.Certificate{}

	var dbCerts []db.Certificate
	var localCerts []db.Certificate
	var err error
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		dbCerts, err = tx.GetCertificates(db.CertificateFilter{})
		return err
	})
	if err != nil {
		logger.Warn("Failed reading certificates from global database", log.Ctx{"err": err})
		return
	}

	for _, dbCert := range dbCerts {
		if _, found := newCerts[dbCert.Type]; !found {
			newCerts[dbCert.Type] = make(map[string]x509.Certificate)
		}

		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			logger.Warn("Failed decoding certificate", log.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Warn("Failed parsing certificate", log.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		newCerts[dbCert.Type][shared.CertFingerprint(cert)] = *cert

		// Add server certs to list of certificates to store in local database to allow cluster restart.
		if dbCert.Type == db.CertificateTypeServer {
			localCerts = append(localCerts, dbCert)
		}
	}

	// Write out the server certs to the local database to allow the cluster to restart.
	err = d.db.Transaction(func(tx *db.NodeTx) error {
		return tx.ReplaceCertificates(localCerts)
	})
	if err != nil {
		logger.Warn("Failed writing certificates to local database", log.Ctx{"err": err})
		// Don't return here, as we still should update the in-memory cache to allow the cluster to
		// continue functioning, and hopefully the write will succeed on next update.
	}

	// Write out the server certs to the local database to allow the cluster to restart.
	err = d.db.Transaction(func(tx *db.NodeTx) error {
		return tx.ReplaceCertificates(localCerts)
	})
	if err != nil {
		logger.Warn("Failed writing certificates to local database", log.Ctx{"err": err})
		// Don't return here, as we still should update the in-memory cache to allow the cluster to
		// continue functioning, and hopefully the write will succeed on next update.
	}

	d.clientCerts.Lock.Lock()
	d.clientCerts.Certificates = newCerts
	d.clientCerts.Lock.Unlock()
}

// updateCertificateCacheFromLocal loads trusted server certificates from local database into memory.
func updateCertificateCacheFromLocal(d *Daemon) error {
	logger.Debug("Refreshing local trusted certificate cache")

	newCerts := map[db.CertificateType]map[string]x509.Certificate{}

	var dbCerts []db.Certificate
	var err error

	err = d.db.Transaction(func(tx *db.NodeTx) error {
		dbCerts, err = tx.GetCertificates()
		return err
	})
	if err != nil {
		return errors.Wrapf(err, "Failed reading certificates from local database")
	}

	for _, dbCert := range dbCerts {
		if _, found := newCerts[dbCert.Type]; !found {
			newCerts[dbCert.Type] = make(map[string]x509.Certificate)
		}

		certBlock, _ := pem.Decode([]byte(dbCert.Certificate))
		if certBlock == nil {
			logger.Warn("Failed decoding certificate", log.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		cert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			logger.Warn("Failed parsing certificate", log.Ctx{"name": dbCert.Name, "err": err})
			continue
		}

		newCerts[dbCert.Type][shared.CertFingerprint(cert)] = *cert
	}

	d.clientCerts.Lock.Lock()
	d.clientCerts.Certificates = newCerts
	d.clientCerts.Lock.Unlock()

	return nil
}

// clusterMemberJoinTokenDecode decodes a base64 and JSON encode join token.
func clusterMemberJoinTokenDecode(input string) (*api.ClusterMemberJoinToken, error) {
	joinTokenJSON, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return nil, err
	}

	var j api.ClusterMemberJoinToken
	err = json.Unmarshal(joinTokenJSON, &j)
	if err != nil {
		return nil, err
	}

	if j.ServerName == "" {
		return nil, fmt.Errorf("No server name in join token")
	}

	if len(j.Addresses) < 1 {
		return nil, fmt.Errorf("No cluster member addresses in join token")
	}

	if j.Secret == "" {
		return nil, fmt.Errorf("No secret in join token")
	}

	if j.Fingerprint == "" {
		return nil, fmt.Errorf("No certificate fingerprint in join token")
	}

	return &j, nil
}

// clusterMemberJoinTokenValid searches for cluster join token that matches the join token provided.
// Returns matching operation if found and cancels the operation, otherwise returns nil.
func clusterMemberJoinTokenValid(d *Daemon, r *http.Request, projectName string, joinToken *api.ClusterMemberJoinToken) (*api.Operation, error) {
	ops, err := operationsGetByType(d, r, projectName, db.OperationClusterJoinToken)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed getting cluster join token operations")
	}

	var foundOp *api.Operation
	for _, op := range ops {
		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		if op.Resources == nil {
			continue
		}

		opSecret, ok := op.Metadata["secret"]
		if !ok {
			continue
		}

		opServerName, ok := op.Metadata["serverName"]
		if !ok {
			continue
		}

		if opServerName == joinToken.ServerName && opSecret == joinToken.Secret {
			foundOp = op
			break
		}
	}

	if foundOp != nil {
		// Token is single-use, so cancel it now.
		err = operationCancel(d, r, projectName, foundOp)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to cancel operation %q", foundOp.ID)
		}

		return foundOp, nil
	}

	// No operation found.
	return nil, nil
}

// certificateTokenValid searches for certificate token that matches the add token provided.
// Returns matching operation if found and cancels the operation, otherwise returns nil.
func certificateTokenValid(d *Daemon, r *http.Request, addToken *api.CertificateAddToken) (*api.Operation, error) {
	ops, err := operationsGetByType(d, r, project.Default, db.OperationCertificateAddToken)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed getting certificate token operations")
	}

	var foundOp *api.Operation
	for _, op := range ops {
		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		opSecret, ok := op.Metadata["secret"]
		if !ok {
			continue
		}

		if opSecret == addToken.Secret {
			foundOp = op
			break
		}
	}

	if foundOp != nil {
		// Token is single-use, so cancel it now.
		err = operationCancel(d, r, project.Default, foundOp)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to cancel operation %q", foundOp.ID)
		}

		return foundOp, nil
	}

	// No operation found.
	return nil, nil
}

// swagger:operation POST /1.0/certificates?public certificates certificates_post_untrusted
//
// Add a trusted certificate
//
// Adds a certificate to the trust store as an untrusted user.
// In this mode, the `password` property must be set to the correct value.
//
// The `certificate` field can be omitted in which case the TLS client
// certificate in use for the connection will be retrieved and added to the
// trust store.
//
// The `?public` part of the URL isn't required, it's simply used to
// separate the two behaviors of this endpoint.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: certificate
//     description: Certificate
//     required: true
//     schema:
//       $ref: "#/definitions/CertificatesPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation POST /1.0/certificates certificates certificates_post
//
// Add a trusted certificate
//
// Adds a certificate to the trust store.
// In this mode, the `password` property is always ignored.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: certificate
//     description: Certificate
//     required: true
//     schema:
//       $ref: "#/definitions/CertificatesPost"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func certificatesPost(d *Daemon, r *http.Request) response.Response {
	// Parse the request.
	req := api.CertificatesPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick check.
	if req.Token && req.Certificate != "" {
		return response.BadRequest(fmt.Errorf("Can't use certificate if token is requested"))
	}

	if req.Token && req.Type != "client" {
		return response.BadRequest(fmt.Errorf("Tokens can only be issued for client certificates"))
	}

	// Access check.
	secret, err := cluster.ConfigGetString(d.cluster, "core.trust_password")
	if err != nil {
		return response.SmartError(err)
	}

	trusted, _, _, err := d.Authenticate(nil, r)
	if err != nil {
		return response.SmartError(err)
	}

	if !rbac.UserIsAdmin(r) {
		if trusted {
			return response.BadRequest(fmt.Errorf("Client is already trusted"))
		}

		if req.Password != "" {
			// Check if cluster member join token supplied as password.
			joinToken, err := clusterMemberJoinTokenDecode(req.Password)
			if err == nil {
				// If so then check there is a matching join operation.
				joinOp, err := clusterMemberJoinTokenValid(d, r, project.Default, joinToken)
				if err != nil {
					return response.InternalError(errors.Wrapf(err, "Failed during search for join token operation"))
				}

				if joinOp == nil {
					return response.Forbidden(fmt.Errorf("No matching cluster join operation found"))
				}
			} else {
				// Check if certificate add token supplied as password.
				joinToken, err := shared.CertificateTokenDecode(req.Password)
				if err == nil {
					// If so then check there is a matching join operation.
					joinOp, err := certificateTokenValid(d, r, joinToken)
					if err != nil {
						return response.InternalError(errors.Wrapf(err, "Failed during search for certificate add token operation"))
					}

					if joinOp == nil {
						return response.Forbidden(fmt.Errorf("No matching certificate add operation found"))
					}

					tokenReq, ok := joinOp.Metadata["request"].(api.CertificatesPost)
					if !ok {
						return response.InternalError(fmt.Errorf("Bad certificate add operation data"))
					}

					// Create a new request from the token data as the user isn't allowed to override anything.
					req = api.CertificatesPost{
						CertificatePut: api.CertificatePut{
							Name:       tokenReq.Name,
							Type:       tokenReq.Type,
							Restricted: tokenReq.Restricted,
							Projects:   tokenReq.Projects,
						},
					}
				} else {
					// Otherwise check if password matches trust password.
					if util.PasswordCheck(secret, req.Password) != nil {
						logger.Warn("Bad trust password", log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
						return response.Forbidden(nil)
					}
				}
			}
		} else {
			return response.Forbidden(nil)
		}
	}

	dbReqType, err := db.CertificateAPITypeToDBType(req.Type)
	if err != nil {
		return response.BadRequest(err)
	}

	// Extract the certificate.
	var cert *x509.Certificate
	if req.Certificate != "" {
		data, err := base64.StdEncoding.DecodeString(req.Certificate)
		if err != nil {
			return response.BadRequest(err)
		}

		cert, err = x509.ParseCertificate(data)
		if err != nil {
			return response.BadRequest(errors.Wrap(err, "invalid certificate material"))
		}
	} else if req.Token {
		// Get all addresses the server is listening on. This is encoded in the certificate token,
		// so that the client will not have to specify a server address. The client will iterate
		// through all these addresses until it can connect to one of them.
		address, err := node.HTTPSAddress(d.db)
		if err != nil {
			return response.InternalError(err)
		}

		addresses, err := util.ListenAddresses(address)
		if err != nil {
			return response.InternalError(err)
		}

		// Generate join secret for new client. This will be stored inside the join token operation and will be
		// supplied by the joining client (encoded inside the join token) which will allow us to lookup the correct
		// operation in order to validate the requested joining client name is correct and authorised.
		joinSecret, err := shared.RandomCryptoString()
		if err != nil {
			return response.InternalError(err)
		}

		// Generate fingerprint of network certificate so joining member can automatically trust the correct
		// certificate when it is presented during the join process.
		fingerprint, err := shared.CertFingerprintStr(string(d.endpoints.NetworkPublicKey()))
		if err != nil {
			return response.InternalError(err)
		}

		if req.Projects == nil {
			req.Projects = []string{}
		}

		meta := map[string]interface{}{
			"secret":      joinSecret,
			"fingerprint": fingerprint,
			"addresses":   addresses,
			"request":     req,
		}

		op, err := operations.OperationCreate(d.State(), project.Default, operations.OperationClassToken, db.OperationCertificateAddToken, nil, meta, nil, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	} else if r.TLS != nil {
		// Add client's certificate.
		if len(r.TLS.PeerCertificates) < 1 {
			// This can happen if the client doesn't send a client certificate or if the server is in
			// CA mode. We rely on this check to prevent non-CA trusted client certificates from being
			// added when in CA mode.
			return response.BadRequest(fmt.Errorf("No client certificate provided"))
		}

		cert = r.TLS.PeerCertificates[len(r.TLS.PeerCertificates)-1]
	} else {
		return response.BadRequest(fmt.Errorf("Can't use TLS data on non-TLS link"))
	}

	// Calculate the fingerprint.
	fingerprint := shared.CertFingerprint(cert)

	// Figure out a name.
	name := req.Name
	if name == "" {
		// Try to pull the CN.
		name = cert.Subject.CommonName
		if name == "" {
			// Fallback to the client's IP address.
			remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				return response.InternalError(err)
			}

			name = remoteHost
		}
	}

	if !isClusterNotification(r) {
		// Check if we already have the certificate.
		existingCert, _ := d.cluster.GetCertificate(fingerprint)
		if existingCert != nil {
			return response.BadRequest(cluster.ErrCertificateExists)
		}

		// Store the certificate in the cluster database.
		dbCert := db.Certificate{
			Fingerprint: shared.CertFingerprint(cert),
			Type:        dbReqType,
			Name:        name,
			Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
		}

		err := d.cluster.CreateCertificate(dbCert)
		if err != nil {
			return response.SmartError(err)
		}

		// Notify other nodes about the new certificate.
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}
		req := api.CertificatesPost{
			CertificatePut: api.CertificatePut{
				Certificate: base64.StdEncoding.EncodeToString(cert.Raw),
			},
		}
		req.Name = name
		req.Type = api.CertificateTypeClient

		err = notifier(func(client lxd.InstanceServer) error {
			return client.CreateCertificate(req)
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Reload the cache.
	updateCertificateCache(d)

	d.State().Events.SendLifecycle(project.Default, lifecycle.CertificateCreated.Event(fingerprint, request.CreateRequestor(r), nil))

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/certificates/%s", version.APIVersion, fingerprint))
}

// swagger:operation GET /1.0/certificates/{fingerprint} certificates certificate_get
//
// Get the trusted certificate
//
// Gets a specific certificate entry from the trust store.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     description: Certificate
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           $ref: "#/definitions/Certificate"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func certificateGet(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	dbCertInfo, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	cert := dbCertInfo.ToAPI()
	return response.SyncResponseETag(true, cert, cert)
}

// swagger:operation PUT /1.0/certificates/{fingerprint} certificates certificate_put
//
// Update the trusted certificate
//
// Updates the entire certificate configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: certificate
//     description: Certificate configuration
//     required: true
//     schema:
//       $ref: "#/definitions/CertificatePut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
func certificatePut(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	// Get current database record.
	oldEntry, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

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

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	// Apply the update.
	return doCertificateUpdate(d, *oldEntry, req, clientType, r)
}

// swagger:operation PATCH /1.0/certificates/{fingerprint} certificates certificate_patch
//
// Partially update the trusted certificate
//
// Updates a subset of the certificate configuration.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: body
//     name: certificate
//     description: Certificate configuration
//     required: true
//     schema:
//       $ref: "#/definitions/CertificatePut"
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "412":
//     $ref: "#/responses/PreconditionFailed"
//   "500":
//     $ref: "#/responses/InternalServerError"
func certificatePatch(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	// Get current database record.
	oldEntry, err := d.cluster.GetCertificate(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

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

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	return doCertificateUpdate(d, *oldEntry, req.Writable(), clientType, r)
}

func doCertificateUpdate(d *Daemon, dbInfo db.Certificate, req api.CertificatePut, clientType clusterRequest.ClientType, r *http.Request) response.Response {
	if clientType == clusterRequest.ClientTypeNormal {
		reqDBType, err := db.CertificateAPITypeToDBType(req.Type)
		if err != nil {
			return response.BadRequest(err)
		}

		// Convert to the database type.
		cert := db.Certificate{
			// Read-only fields.
			Certificate: dbInfo.Certificate,
			Fingerprint: dbInfo.Fingerprint,
			Name:        req.Name,
			Type:        reqDBType,
		}

		// Update the database record.
		err = d.cluster.UpdateCertificate(dbInfo.Fingerprint, cert)
		if err != nil {
			return response.SmartError(err)
		}

		// Notify other nodes about the new certificate.
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}

		err = notifier(func(client lxd.InstanceServer) error {
			return client.UpdateCertificate(dbInfo.Fingerprint, req, "")
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Reload the cache.
	updateCertificateCache(d)

	d.State().Events.SendLifecycle(project.Default, lifecycle.CertificateUpdated.Event(dbInfo.Fingerprint, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/certificates/{fingerprint} certificates certificate_delete
//
// Delete the trusted certificate
//
// Removes the certificate from the trust store.
//
// ---
// produces:
//   - application/json
// responses:
//   "200":
//     $ref: "#/responses/EmptySyncResponse"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func certificateDelete(d *Daemon, r *http.Request) response.Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	if !isClusterNotification(r) {
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

		// Notify other nodes about the new certificate.
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAlive)
		if err != nil {
			return response.SmartError(err)
		}

		err = notifier(func(client lxd.InstanceServer) error {
			return client.DeleteCertificate(certInfo.Fingerprint)
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Reload the cache.
	updateCertificateCache(d)

	d.State().Events.SendLifecycle(project.Default, lifecycle.CertificateDeleted.Event(fingerprint, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
