package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	dqliteClient "github.com/canonical/go-dqlite/v3/client"
	"github.com/canonical/go-dqlite/v3/driver"
	"github.com/gorilla/mux"
	liblxc "github.com/lxc/go-lxc"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/acme"
	"github.com/canonical/lxd/lxd/apparmor"
	"github.com/canonical/lxd/lxd/auth"
	authDrivers "github.com/canonical/lxd/lxd/auth/drivers"
	"github.com/canonical/lxd/lxd/auth/oidc"
	"github.com/canonical/lxd/lxd/bgp"
	"github.com/canonical/lxd/lxd/cluster"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/config"
	"github.com/canonical/lxd/lxd/daemon"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/openfga"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/dns"
	"github.com/canonical/lxd/lxd/endpoints"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/firewall"
	"github.com/canonical/lxd/lxd/fsmonitor"
	fsmonitorDrivers "github.com/canonical/lxd/lxd/fsmonitor/drivers"
	"github.com/canonical/lxd/lxd/identity"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance"
	instanceDrivers "github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/loki"
	"github.com/canonical/lxd/lxd/maas"
	"github.com/canonical/lxd/lxd/metrics"
	networkZone "github.com/canonical/lxd/lxd/network/zone"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/rsync"
	"github.com/canonical/lxd/lxd/seccomp"
	"github.com/canonical/lxd/lxd/state"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/lxd/storage/s3/miniod"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/ubuntupro"
	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// secFetchSiteForbidden defines client Sec-Fetch-Site header values that will be forbidden access.
var secFetchSiteForbidden = []string{"cross-site", "same-site"}

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	identityCache *identity.Cache
	os            *sys.OS
	db            *db.DB
	firewall      firewall.Firewall
	maas          *maas.Controller
	bgp           *bgp.Server
	dns           *dns.Server

	// Event servers
	devLXDEvents     *events.DevLXDServer
	events           *events.Server
	internalListener *events.InternalListener

	// Tasks registry for long-running background tasks
	// Keep clustering tasks separate as they cause a lot of CPU wakeups
	tasks        *task.Group
	clusterTasks *task.Group

	// Indexes of tasks that need to be reset when their execution interval changes
	taskPruneImages      *task.Task
	taskClusterHeartbeat *task.Task

	// Stores startup time of daemon
	startTime time.Time

	// Whether daemon was started by systemd socket activation.
	systemdSocketActivated bool

	config    *DaemonConfig
	endpoints *endpoints.Endpoints
	gateway   *cluster.Gateway
	seccomp   *seccomp.Server

	proxy func(req *http.Request) (*url.URL, error)

	oidcVerifier *oidc.Verifier

	// Stores last heartbeat node information to detect node changes.
	lastNodeList *cluster.APIHeartbeat

	// Serialize changes to cluster membership (joins, leaves, role
	// changes).
	clusterMembershipMutex sync.RWMutex

	serverCert    func() *shared.CertInfo
	serverCertInt *shared.CertInfo // Do not use this directly, use servertCert func.

	// Status control.
	startStopLock    sync.Mutex       // Prevent concurrent starts and stops.
	setupChan        chan struct{}    // Closed when basic Daemon setup is completed
	waitReady        cancel.Canceller // Cancelled when LXD is fully ready
	waitNetworkReady cancel.Canceller // Closed when all networks are ready.
	waitStorageReady cancel.Canceller // Closed when all storage pools are ready.
	shutdownCtx      cancel.Canceller // Cancelled when shutdown starts.
	shutdownDoneCh   chan error       // Receives the result of the d.Stop() function and tells LXD to end.

	// Device monitor for watching filesystem events
	devmonitor fsmonitor.FSMonitor

	// Keep track of skews.
	timeSkew bool

	// Configuration.
	globalConfig   *clusterConfig.Config
	localConfig    *node.Config
	globalConfigMu sync.Mutex

	// Cluster.
	serverName      string
	serverClustered bool

	lokiClient *loki.Client

	// HTTP-01 challenge provider for ACME
	http01Provider acme.HTTP01Provider

	// Authorization.
	authorizer auth.Authorizer

	// Syslog listener cancel function.
	syslogSocketCancel context.CancelFunc

	// Ubuntu Pro settings
	ubuntuPro *ubuntupro.Client

	// internalSecrets holds the current in-memory value of the secrets
	internalSecrets   dbCluster.AuthSecrets
	internalSecretsMu sync.Mutex
}

// DaemonConfig holds configuration values for Daemon.
type DaemonConfig struct {
	Group              string        // Group name the local unix socket should be chown'ed to
	Trace              []string      // List of sub-systems to trace
	RaftLatency        float64       // Coarse grain measure of the cluster latency
	DqliteSetupTimeout time.Duration // How long to wait for the cluster database to be up
}

// newDaemon returns a new Daemon object with the given configuration.
func newDaemon(config *DaemonConfig, os *sys.OS) *Daemon {
	shutdownCtx := cancel.New()

	d := &Daemon{
		identityCache:    &identity.Cache{},
		config:           config,
		tasks:            task.NewGroup(),
		clusterTasks:     task.NewGroup(),
		db:               &db.DB{},
		http01Provider:   acme.NewHTTP01Provider(),
		os:               os,
		setupChan:        make(chan struct{}),
		waitReady:        cancel.New(),
		waitNetworkReady: cancel.New(),
		waitStorageReady: cancel.New(),
		shutdownCtx:      shutdownCtx,
		shutdownDoneCh:   make(chan error),
	}

	d.serverCert = func() *shared.CertInfo { return d.serverCertInt }

	return d
}

// defaultDaemonConfig returns a DaemonConfig object with default values.
func defaultDaemonConfig() *DaemonConfig {
	return &DaemonConfig{
		RaftLatency:        3.0,
		DqliteSetupTimeout: 36 * time.Hour, // Account for snap refresh lag
	}
}

// defaultDaemon returns a new, un-initialized Daemon object with default values.
func defaultDaemon() *Daemon {
	config := defaultDaemonConfig()
	os := sys.DefaultOS()
	return newDaemon(config, os)
}

// APIEndpoint represents a URL in our API.
type APIEndpoint struct {
	Name        string             // Name for this endpoint.
	Path        string             // Path pattern for this endpoint.
	MetricsType entity.Type        // Main entity type related to this endpoint. Used by the API metrics.
	Aliases     []APIEndpointAlias // Any aliases for this endpoint.
	Get         APIEndpointAction
	Head        APIEndpointAction
	Put         APIEndpointAction
	Post        APIEndpointAction
	Delete      APIEndpointAction
	Patch       APIEndpointAction
}

// APIEndpointAlias represents an alias URL of and APIEndpoint in our API.
type APIEndpointAlias struct {
	Name string // Name for this alias.
	Path string // Path pattern for this alias.
}

// APIEndpointAction represents an action on an API endpoint.
type APIEndpointAction struct {
	Handler        func(d *Daemon, r *http.Request) response.Response
	AccessHandler  func(d *Daemon, r *http.Request) response.Response
	AllowUntrusted bool
	ContentTypes   []string // Client content types to allow.
}

// allowAuthenticated is an AccessHandler which allows only authenticated requests. This should be used in conjunction
// with further access control within the handler (e.g. to filter resources the user is able to view/edit).
func allowAuthenticated(_ *Daemon, r *http.Request) response.Response {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	if requestor.IsTrusted() {
		return response.EmptySyncResponse
	}

	return response.Forbidden(nil)
}

// allowPermission is a wrapper to check access against a given object, an object being an image, instance, network, etc.
// Mux vars should be passed in so that the object we are checking can be created. For example, a certificate object requires
// a fingerprint, the mux var for certificate fingerprints is "fingerprint", so that string should be passed in.
// Mux vars should always be passed in with the same order they appear in the API route.
func allowPermission(entityType entity.Type, entitlement auth.Entitlement, muxVars ...string) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		var err error
		var entityURL *api.URL
		if entityType == entity.TypeServer {
			// For server permission checks, skip mux var logic.
			entityURL = entity.ServerURL()
		} else if entityType == entity.TypeProject && len(muxVars) == 0 {
			// If we're checking project permissions on a non-project endpoint (e.g. `can_create_instances` on POST /1.0/instances)
			// we get the project name from the query parameter.
			// If we're checking project permissions on a project endpoint, we expect to get the project name from its path variable
			// in the next else block.
			entityURL = entity.ProjectURL(request.ProjectParam(r))
		} else {
			muxValues := make([]string, 0, len(muxVars))
			vars := mux.Vars(r)
			for _, muxVar := range muxVars {
				muxValue := vars[muxVar]
				if muxValue == "" {
					return response.InternalError(fmt.Errorf("Failed to perform permission check: Path argument label %q not found in request URL %q", muxVar, r.URL))
				}

				muxValues = append(muxValues, muxValue)
			}

			entityURL, err = entityType.URL(request.QueryParam(r, "project"), request.QueryParam(r, "target"), muxValues...)
			if err != nil {
				return response.InternalError(fmt.Errorf("Failed to perform permission check: %w", err))
			}
		}

		// Validate whether the user has the needed permission
		err = s.Authorizer.CheckPermission(r.Context(), entityURL, entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
}

// allowProjectResourceList should be used instead of allowAuthenticated when listing resources within a project.
// This prevents a restricted TLS client from listing resources in a project that they do not have access to.
func allowProjectResourceList(d *Daemon, r *http.Request) response.Response {
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	// The caller must be authenticated.
	if !requestor.IsTrusted() {
		return response.Forbidden(nil)
	}

	// A root user can list resources in any project.
	if requestor.IsAdmin() {
		return response.EmptySyncResponse
	}

	id := requestor.CallerIdentity()
	if id == nil {
		return response.InternalError(errors.New("No identity present in request details"))
	}

	idType := requestor.CallerIdentityType()
	if idType == nil {
		return response.InternalError(errors.New("No identity type present in request details"))
	}

	if idType.IsFineGrained() {
		// Fine-grained clients can call the endpoint but may see an empty list.
		return response.EmptySyncResponse
	}

	// We should now only be left with restricted client certificates. Metrics certificates should have been disregarded
	// already, because they cannot call any endpoint other than /1.0/metrics (which is enforced during authentication).
	if idType.Name() != api.IdentityTypeCertificateClientRestricted {
		return response.InternalError(fmt.Errorf("Encountered unexpected identity type %q listing resources", idType.Name()))
	}

	requestProjectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	// all-projects requests are not allowed
	if allProjects {
		return response.Forbidden(errors.New("Certificate is restricted"))
	}

	// Disallow listing resources in projects the caller does not have access to.
	if !slices.Contains(id.Projects, requestProjectName) {
		return response.Forbidden(errors.New("Certificate is restricted"))
	}

	return response.EmptySyncResponse
}

// reportEntitlements takes a map of entity URLs to EntitlementReporters (in practice, API types that implement the ReportEntitlements method), and
// reports the entitlements that the caller has on each entity URL to the corresponding EntitlementReporter.
func reportEntitlements(ctx context.Context, authorizer auth.Authorizer, entityType entity.Type, requestedEntitlements []auth.Entitlement, entityURLToEntitlementReporter map[*api.URL]auth.EntitlementReporter) error {
	// Nothing to do
	if len(entityURLToEntitlementReporter) == 0 {
		return nil
	}

	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return err
	}

	// No fine-grained identities are global admins. Check this first in case the caller is using e.g. the unix socket.
	if requestor.IsAdmin() {
		return api.NewStatusError(http.StatusBadRequest, "Cannot report entitlements for identities that do not use fine-grained authorization")
	}

	// Any other requestor should have an identity type present.
	identityType := requestor.CallerIdentityType()
	if identityType == nil {
		return errors.New("No identity type present in request details")
	}

	// Check the identity type is fine-grained (it could be a restricted client certificate).
	if !identityType.IsFineGrained() {
		return api.NewStatusError(http.StatusBadRequest, "Cannot report entitlements for identities that do not use fine-grained authorization")
	}

	// In the case where we have only one entity URL, we'll use the authorizer's CheckPermission method
	// whereas if we have multiple entity URLs, we'll use the authorizer's GetPermissionChecker method that
	// is more efficient for returning entitlements for a batch of entities.
	if len(entityURLToEntitlementReporter) == 1 {
		for u, r := range entityURLToEntitlementReporter {
			entitlements := make([]string, 0, len(requestedEntitlements))
			for _, entitlement := range requestedEntitlements {
				err = authorizer.CheckPermission(ctx, u, entitlement)
				if err != nil {
					if auth.IsDeniedError(err) {
						continue
					}

					return fmt.Errorf("Failed to check entitlement %q for entity URL %q: %w", entitlement, u, err)
				}

				entitlements = append(entitlements, string(entitlement))
			}

			r.ReportEntitlements(entitlements)
		}

		return nil
	}

	checkersByEntitlement := make(map[auth.Entitlement]auth.PermissionChecker)
	for _, entitlement := range requestedEntitlements {
		checker, err := authorizer.GetPermissionChecker(ctx, entitlement, entityType)
		if err != nil {
			return fmt.Errorf("Failed to get a permission checker for entitlement %q and for entity type %q: %w", entitlement, entityType, err)
		}

		checkersByEntitlement[entitlement] = checker
	}

	for u, reporter := range entityURLToEntitlementReporter {
		entitlements := make([]string, 0, len(requestedEntitlements))
		for entitlement, checker := range checkersByEntitlement {
			if checker(u) {
				entitlements = append(entitlements, string(entitlement))
			}
		}

		reporter.ReportEntitlements(entitlements)
	}

	return nil
}

// extractEntitlementsFromQuery extracts the entitlements from the query string of the request.
func extractEntitlementsFromQuery(r *http.Request, entityType entity.Type, allowRecursion bool) ([]auth.Entitlement, error) {
	rawEntitlements := request.QueryParam(r, "with-access-entitlements")
	if rawEntitlements == "" {
		return nil, nil
	}

	allowedEntitlements := auth.EntityTypeToEntitlements[entityType]
	entitlements := strings.Split(rawEntitlements, ",")
	validEntitlements := make([]auth.Entitlement, 0, len(entitlements))
	for _, e := range entitlements {
		if !slices.Contains(allowedEntitlements, auth.Entitlement(e)) {
			return nil, api.StatusErrorf(http.StatusBadRequest, "Requested entitlement %q is not valid for entity type %q", e, entityType)
		}

		validEntitlements = append(validEntitlements, auth.Entitlement(e))
	}

	// Entitlements can only be requested when recursion is enabled for a request returning multiple entities (this function call uses `allowRecursion=true`).
	// If the request is meant to return a single entity, the entitlements can be requested regardless of the recursion setting (in this case, the function is called with `allowRecursion=false`).
	if len(validEntitlements) > 0 && (!util.IsRecursionRequest(r) && allowRecursion) {
		return nil, errors.New("Entitlements can only be requested when recursion is enabled")
	}

	return validEntitlements, nil
}

// Authenticate validates an incoming http Request
// It will check over what protocol it came, what type of request it is and
// will validate the TLS certificate or OIDC token.
//
// This does not perform authorization, only validates authentication.
// Returns whether trusted or not, the username (or certificate fingerprint) of the trusted client, and the type of
// client that has been authenticated (cluster, unix, oidc or tls).
func (d *Daemon) Authenticate(w http.ResponseWriter, r *http.Request) (*request.RequestorArgs, error) {
	// Perform mTLS check against server certificates. If this passes, the request was made by another cluster member
	// and the protocol is [request.ProtocolCluster].
	if r.TLS != nil {
		for _, i := range r.TLS.PeerCertificates {
			trusted, fingerprint := util.CheckMutualTLS(*i, d.identityCache.X509Certificates(api.IdentityTypeCertificateServer))
			if trusted {
				return &request.RequestorArgs{
					Trusted:  true,
					Username: fingerprint,
					Protocol: request.ProtocolCluster,
				}, nil
			}
		}
	}

	// Local unix socket queries.
	if r.RemoteAddr == "@" && r.TLS == nil {
		cred, err := ucred.GetCredFromContext(r.Context())
		if err != nil {
			return nil, err
		}

		uid := strconv.FormatUint(uint64(cred.Uid), 10)
		username := "uid=" + uid

		u, err := user.LookupId(uid)
		if err == nil {
			username = u.Username
		}

		return &request.RequestorArgs{
			Trusted:  true,
			Username: username,
			Protocol: request.ProtocolUnix,
		}, nil
	}

	// Bad query, no TLS found.
	if r.TLS == nil {
		return nil, errors.New("Bad/missing TLS on network query")
	}

	if d.oidcVerifier != nil && d.oidcVerifier.IsRequest(r) {
		result, err := d.oidcVerifier.Auth(w, r)
		if err != nil {
			return nil, fmt.Errorf("Failed OIDC Authentication: %w", err)
		}

		err = d.handleOIDCAuthenticationResult(r, result)
		if err != nil {
			return nil, fmt.Errorf("Failed to process OIDC authentication result: %w", err)
		}

		return &request.RequestorArgs{
			Trusted:                true,
			Username:               result.Email,
			Protocol:               api.AuthenticationMethodOIDC,
			IdentityProviderGroups: result.IdentityProviderGroups,
		}, nil
	}

	isMetricsRequest := func(u url.URL) bool {
		return strings.HasPrefix(u.Path, "/1.0/metrics")
	}

	// List of candidate identity types for this request. We have already checked server certificates at the beginning of this method
	// so we only need to consider client and metrics certificates. (OIDC auth was completed above).
	candidateIdentityTypes := []string{api.IdentityTypeCertificateClientUnrestricted, api.IdentityTypeCertificateClientRestricted, api.IdentityTypeCertificateClient}
	if isMetricsRequest(*r.URL) {
		// Metrics certificates can only authenticate when calling metrics related endpoints.
		candidateIdentityTypes = append(candidateIdentityTypes, api.IdentityTypeCertificateMetricsUnrestricted, api.IdentityTypeCertificateMetricsRestricted)
	}

	// Map of candidate certificates of mTLS check.
	candidateCertificates := make(map[string]x509.Certificate)

	// If the network cert has a CA, validate the peer certificates against it.
	if d.endpoints.NetworkCert().CA() != nil {
		trustCACertificates := d.globalConfig.TrustCACertificates()
		for _, peerCertificate := range r.TLS.PeerCertificates {
			trusted, _, fingerprint := util.CheckCASignature(*peerCertificate, d.endpoints.NetworkCert())
			if !trusted {
				return &request.RequestorArgs{Trusted: false}, nil
			}

			// Check if a matching certificate is present in the identity cache.
			id, err := d.identityCache.Get(api.AuthenticationMethodTLS, fingerprint)
			if err != nil {
				if !api.StatusErrorCheck(err, http.StatusNotFound) {
					return nil, err
				}

				// If we have a not found error and `core.trust_ca_certificates` is true, then the identity is implicitly
				// trusted because their certificate was signed by the CA.
				if trustCACertificates {
					return &request.RequestorArgs{
						Trusted:  true,
						Username: fingerprint,
						Protocol: request.ProtocolPKI,
					}, nil
				}

				// If we don't implicitly trust CA signed certificates, then the identity is not trusted because they
				// are not present in the identity cache.
				return &request.RequestorArgs{Trusted: false}, nil
			}

			// The identity type must be in our list of candidate types (e.g. if this certificate is a metrics certificate
			// and we're on a non-metrics related route).
			if !slices.Contains(candidateIdentityTypes, id.IdentityType) {
				return &request.RequestorArgs{Trusted: false}, nil
			}

			// In CA mode we only consider if this exact certificate is valid via mTLS checks below.
			candidateCertificates[id.Identifier] = *id.Certificate
		}
	} else {
		// In non-CA mode we consider all certificates that would be valid for this API route.
		candidateCertificates = d.identityCache.X509Certificates(candidateIdentityTypes...)
	}

	// Perform mTLS check on candidates.
	for _, i := range r.TLS.PeerCertificates {
		trusted, fingerprint := util.CheckMutualTLS(*i, candidateCertificates)
		if trusted {
			return &request.RequestorArgs{
				Trusted:  true,
				Username: fingerprint,
				Protocol: api.AuthenticationMethodTLS,
			}, nil
		}
	}

	// Reject unauthorized.
	return &request.RequestorArgs{Trusted: false}, nil
}

// handleOIDCAuthenticationResult checks the identity cache for the OIDC identity by their email address. If no identity
// is found, an identity is added with that email. If an identity is found but the OIDC subject is different to the
// expected value, the identity is updated with the new subject.
func (d *Daemon) handleOIDCAuthenticationResult(r *http.Request, result *oidc.AuthenticationResult) error {
	var action lifecycle.IdentityAction

	id, err := d.identityCache.Get(api.AuthenticationMethodOIDC, result.Email)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed getting OIDC identity from cache: %w", err)
	} else if err != nil {
		// Identity not found. Add it to the database and refresh the identity cache.
		idMetadata := dbCluster.OIDCMetadata{Subject: result.Subject}
		b, err := json.Marshal(idMetadata)
		if err != nil {
			return fmt.Errorf("Failed to marshal OIDC identity metadata: %w", err)
		}

		err = d.db.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := dbCluster.CreateIdentity(ctx, tx.Tx(), dbCluster.Identity{
				AuthMethod: api.AuthenticationMethodOIDC,
				Type:       api.IdentityTypeOIDCClient,
				Identifier: result.Email,
				Name:       result.Name,
				Metadata:   string(b),
			})
			return err
		})
		if err != nil {
			return fmt.Errorf("Failed to add new OIDC identity to database: %w", err)
		}

		action = lifecycle.IdentityCreated
	} else if id.Subject != result.Subject || id.Name != result.Name {
		// The OIDC subject of the user with this email address has changed (this should be rare). Replace the
		// subject in the identity metadata and refresh the cache.
		idMetadata := dbCluster.OIDCMetadata{Subject: result.Subject}
		b, err := json.Marshal(idMetadata)
		if err != nil {
			return fmt.Errorf("Failed to marshal OIDC identity metadata: %w", err)
		}

		err = d.db.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			return dbCluster.UpdateIdentity(ctx, tx.Tx(), api.AuthenticationMethodOIDC, result.Email, dbCluster.Identity{
				AuthMethod: api.AuthenticationMethodOIDC,
				Type:       api.IdentityTypeOIDCClient,
				Identifier: result.Email,
				Name:       result.Name,
				Metadata:   string(b),
			})
		})
		if err != nil {
			return fmt.Errorf("Failed to update OIDC identity information: %w", err)
		}

		action = lifecycle.IdentityUpdated
	}

	if action != "" {
		// Notify other nodes about the new identity.
		s := d.State()
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAlive)
		if err != nil {
			return fmt.Errorf("Failed to notify cluster members of new or updated OIDC identity: %w", err)
		}

		err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
			_, _, err := client.RawQuery(http.MethodPost, "/internal/identity-cache-refresh", nil, "")
			return err
		})
		if err != nil {
			return fmt.Errorf("Failed to notify cluster members of new or updated OIDC identity: %w", err)
		}

		lc := action.Event(api.AuthenticationMethodOIDC, result.Email, request.CreateRequestor(r.Context()), nil)
		s.Events.SendLifecycle("", lc)

		s.UpdateIdentityCache()
	}

	return nil
}

// getCoreAuthSecrets gets a copy of the current, cluster-wide secrets. The approach can be summarized as follows:
// 1. Check if the current in-memory value is valid. If valid, return a copy.
// 2. Check if the current in-database value is valid. If valid, replace in-memory value and return a copy.
// 3. Rotate the in-database value within the transaction, then replace in-memory value and return a copy.
// Steps 2 and 3 must happen within the same transaction so that database locking enforces consistency across the
// cluster. Everything is performed with a lock on the in-memory value. Note that this approach assumes that the UTC
// time is synchronized across all cluster members, as this is used for validity checking.
func (d *Daemon) getCoreAuthSecrets(ctx context.Context) (dbCluster.AuthSecrets, error) {
	// Obtain a lock.
	d.internalSecretsMu.Lock()
	defer d.internalSecretsMu.Unlock()

	// Get the expiry.
	expiry := d.globalConfig.AuthSecretExpiry()

	// Check if the current in-memory secrets are valid.
	err := d.internalSecrets.Validate(expiry)
	if err == nil {
		// If valid, return a copy.
		return slices.Clone(d.internalSecrets), nil
	}

	// Otherwise, start a transaction.
	err = d.db.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the secrets.
		secrets, err := dbCluster.GetCoreAuthSecrets(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Check if the secrets are valid (if no secrets were found, then they are not valid).
		err = secrets.Validate(expiry)
		if err == nil {
			// If valid, set internal secrets to the value defined in the database and exit transaction.
			d.internalSecrets = secrets
			return nil
		}

		// Rotate the secrets. If there were none, this will add the first value.
		rotatedSecrets, err := secrets.Rotate(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Set internal secrets to the new value and exit transaction.
		d.internalSecrets = rotatedSecrets
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Above transaction set the internal secrets to a new valid value. Return a copy.
	return slices.Clone(d.internalSecrets), nil
}

// State creates a new State instance linked to our internal db and os.
func (d *Daemon) State() *state.State {
	// If the daemon is shutting down, the context will be cancelled.
	// This information will be available throughout the code, and can be used to prevent new
	// operations from starting during shutdown.

	// Build a list of instance types.
	drivers := instanceDrivers.DriverStatuses()
	instanceTypes := make(map[instancetype.Type]error, len(drivers))
	for driverType, driver := range drivers {
		instanceTypes[driverType] = driver.Info.Error
	}

	d.globalConfigMu.Lock()
	globalConfig := d.globalConfig
	localConfig := d.localConfig
	d.globalConfigMu.Unlock()

	s := &state.State{
		ShutdownCtx:         d.shutdownCtx,
		DB:                  d.db,
		MAAS:                d.maas,
		BGP:                 d.bgp,
		DNS:                 d.dns,
		OS:                  d.os,
		Endpoints:           d.endpoints,
		Events:              d.events,
		DevlxdEvents:        d.devLXDEvents,
		Firewall:            d.firewall,
		Proxy:               d.proxy,
		ServerCert:          d.serverCert,
		UpdateIdentityCache: func() { updateIdentityCache(d) },
		IdentityCache:       d.identityCache,
		InstanceTypes:       instanceTypes,
		DevMonitor:          d.devmonitor,
		GlobalConfig:        globalConfig,
		LocalConfig:         localConfig,
		ServerName:          d.serverName,
		ServerClustered:     d.serverClustered,
		StartTime:           d.startTime,
		Authorizer:          d.authorizer,
		UbuntuPro:           d.ubuntuPro,
		NetworkReady:        d.waitNetworkReady,
		StorageReady:        d.waitStorageReady,
		CoreAuthSecrets:     d.getCoreAuthSecrets,
	}

	s.LeaderInfo = func() (*state.LeaderInfo, error) {
		if !s.ServerClustered {
			return &state.LeaderInfo{
				Clustered: false,
				Leader:    true,
				Address:   "",
			}, nil
		}

		localClusterAddress := s.LocalConfig.ClusterAddress()
		leaderAddress, err := d.gateway.LeaderAddress()
		if err != nil {
			return nil, fmt.Errorf("Failed to get the address of the cluster leader: %w", err)
		}

		return &state.LeaderInfo{
			Clustered: true,
			Leader:    localClusterAddress == leaderAddress,
			Address:   leaderAddress,
		}, nil
	}

	s.ImagesStoragePath = func(projectName string) string {
		return daemonStoragePath(s.LocalConfig.StorageImagesVolume(projectName), config.DaemonStorageTypeImages)
	}

	s.BackupsStoragePath = func(projectName string) string {
		return daemonStoragePath(s.LocalConfig.StorageBackupsVolume(projectName), config.DaemonStorageTypeBackups)
	}

	return s
}

// createCmd creates API handlers for the provided endpoint including some useful behavior,
// such as appropriate authentication, authorization and checking server availability.
//
// The created handler also keeps track of handled requests for the API metrics
// for the main API endpoints.
func (d *Daemon) createCmd(restAPI *mux.Router, version string, c APIEndpoint) {
	var uri string
	if c.Path == "" {
		uri = "/" + version
	} else if version != "" {
		uri = "/" + version + "/" + c.Path
	} else {
		uri = "/" + c.Path
	}

	route := restAPI.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {
		// Only endpoints from the main API (version 1.0) should be counted for the metrics.
		// This prevents internal endpoints from being included as well.
		if version == "1.0" {
			metrics.TrackStartedRequest(r, c.MetricsType)
		}

		w.Header().Set("Content-Type", "application/json")

		if r.RemoteAddr != "@" || version != "internal" {
			// Block public API requests until we're done with basic
			// initialization tasks, such setting up the cluster database.
			select {
			case <-d.setupChan:
			default:
				response := response.Unavailable(errors.New("LXD daemon setup in progress"))
				_ = response.Render(w, r)
				return
			}
		}

		// Authentication
		requestor, err := d.Authenticate(w, r)
		if err != nil {
			var authError oidc.AuthError
			if errors.As(err, &authError) {
				// Ensure the OIDC headers are set if needed.
				if d.oidcVerifier != nil {
					_ = d.oidcVerifier.WriteHeaders(w)
				}

				// Return 401 Unauthorized error. This indicates to the client that it needs to use the
				// headers we've set above to get an access token and try again.
				_ = response.Unauthorized(err).Render(w, r)
				return
			}

			_ = response.Forbidden(err).Render(w, r)
			return
		}

		// Initialise the request info.
		err = request.SetRequestor(r, d.identityCache, *requestor)
		if err != nil {
			_ = response.SmartError(err).Render(w, r)
			return
		}

		// Reject internal queries to remote, non-cluster, clients
		if version == "internal" && !slices.Contains([]string{request.ProtocolUnix, request.ProtocolCluster}, requestor.Protocol) {
			// Except for the initial cluster accept request (done over trusted TLS)
			if !requestor.Trusted || c.Path != "cluster/accept" || requestor.Protocol != api.AuthenticationMethodTLS {
				logger.Warn("Rejecting remote internal API request", logger.Ctx{"ip": r.RemoteAddr})
				_ = response.Forbidden(nil).Render(w, r)
				return
			}
		}

		logCtx := logger.Ctx{"method": r.Method, "url": r.URL.RequestURI(), "ip": r.RemoteAddr, "protocol": requestor.Protocol}
		if requestor.Protocol == request.ProtocolCluster {
			logCtx["fingerprint"] = requestor.Username
		} else {
			logCtx["username"] = requestor.Username
		}

		untrustedOk := (r.Method == "GET" && c.Get.AllowUntrusted) || (r.Method == "POST" && c.Post.AllowUntrusted)
		if requestor.Trusted {
			logger.Debug("Handling API request", logCtx)
		} else if untrustedOk && r.Header.Get("X-LXD-authenticated") == "" {
			logger.Debug("Allowing untrusted "+r.Method, logger.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else {
			if d.oidcVerifier != nil {
				_ = d.oidcVerifier.WriteHeaders(w)
			}

			logger.Warn("Rejecting request from untrusted client", logger.Ctx{"ip": r.RemoteAddr})
			_ = response.Forbidden(nil).Render(w, r)
			return
		}

		// Set OpenFGA cache in request context.
		request.SetContextValue(r, request.CtxOpenFGARequestCache, &openfga.RequestCache{})

		// Dump full request JSON when in debug mode
		if daemon.Debug && r.Method != "GET" && util.IsJSONRequest(r) {
			newBody := &bytes.Buffer{}
			captured := &bytes.Buffer{}
			multiW := io.MultiWriter(newBody, captured)
			_, err := io.Copy(multiW, r.Body)
			if err != nil {
				_ = response.InternalError(err).Render(w, r)
				return
			}

			r.Body = shared.BytesReadCloser{Buf: newBody}
			util.DebugJSON("API Request", captured, logger.AddContext(logCtx))
		}

		// Actually process the request
		var resp response.Response

		// Return Unavailable Error (503) if daemon is shutting down.
		// There are some exceptions:
		// - internal calls, e.g. lxd shutdown
		// - events endpoint as this is accessed when running `lxd shutdown`
		// - /1.0 endpoint
		// - /1.0/operations endpoints
		// - GET queries
		allowedDuringShutdown := func() bool {
			if version == "internal" {
				return true
			}

			if c.Path == "" || c.Path == "events" || c.Path == "operations" || strings.HasPrefix(c.Path, "operations/") {
				return true
			}

			if r.Method == "GET" {
				return true
			}

			return false
		}

		if d.shutdownCtx.Err() == context.Canceled && !allowedDuringShutdown() {
			_ = response.Unavailable(errors.New("LXD is shutting down")).Render(w, r)
			return
		}

		handleRequest := func(action APIEndpointAction) response.Response {
			if action.Handler == nil {
				return response.NotImplemented(nil)
			}

			// Protect against CSRF when using LXD-UI with browser that supports Fetch metadata.
			// Deny Sec-Fetch-Site when set to cross-site or same-site.
			if slices.Contains(secFetchSiteForbidden, r.Header.Get("Sec-Fetch-Site")) {
				return response.ErrorResponse(http.StatusForbidden, "Forbidden Sec-Fetch-Site header value")
			}

			if len(action.ContentTypes) == 0 {
				// Require application/json if not specified by handler.
				action.ContentTypes = []string{"application/json"}
			}

			// Validate browser Content-Type if supplied, or if non-zero Content-Length supplied.
			if isBrowserClient(r) {
				contentTypeParts := shared.SplitNTrimSpace(r.Header.Get("Content-Type"), ";", 2, false) // Ignore multi-part boundary part.
				contentLength := r.Header.Get("Content-Length")
				hasContentLength := contentLength != "" && contentLength != "0"
				if (hasContentLength || contentTypeParts[0] != "") && !slices.Contains(action.ContentTypes, contentTypeParts[0]) {
					return response.ErrorResponse(http.StatusUnsupportedMediaType, "Unsupported Content-Type for this request")
				}
			}

			// All APIEndpointActions should have an access handler or should allow untrusted requests.
			if action.AccessHandler == nil && !action.AllowUntrusted {
				return response.InternalError(fmt.Errorf("Access handler not defined for %s %s", r.Method, r.URL.RequestURI()))
			}

			// If the request is not trusted, only call the handler if the action allows it.
			if !requestor.Trusted && !action.AllowUntrusted {
				return response.Forbidden(errors.New("You must be authenticated"))
			}

			// Call the access handler if there is one.
			if action.AccessHandler != nil {
				resp := action.AccessHandler(d, r)
				if resp != response.EmptySyncResponse {
					return resp
				}
			}

			return action.Handler(d, r)
		}

		switch r.Method {
		case "GET":
			resp = handleRequest(c.Get)
		case "HEAD":
			resp = handleRequest(c.Head)
		case "PUT":
			resp = handleRequest(c.Put)
		case "POST":
			resp = handleRequest(c.Post)
		case "DELETE":
			resp = handleRequest(c.Delete)
		case "PATCH":
			resp = handleRequest(c.Patch)
		default:
			resp = response.NotFound(fmt.Errorf("Method %q not found", r.Method))
		}

		// Handle errors
		err = resp.Render(w, r)
		if err != nil {
			writeErr := response.SmartError(err).Render(w, r)
			if writeErr != nil {
				logger.Warn("Failed writing error for HTTP response", logger.Ctx{"url": uri, "err": err, "writeErr": writeErr})
			}
		}
	})

	// If the endpoint has a canonical name then record it so it can be used to build URLS
	// and accessed in the context of the request by the handler function.
	if c.Name != "" {
		route.Name(c.Name)
	}
}

// have we setup shared mounts?
var sharedMountsLock sync.Mutex

// setupSharedMounts will mount any shared mounts needed, and set daemon.SharedMountsSetup to true.
func setupSharedMounts() error {
	// Check if we already went through this
	if daemon.SharedMountsSetup {
		return nil
	}

	// Get a lock to prevent races
	sharedMountsLock.Lock()
	defer sharedMountsLock.Unlock()

	// Check if already setup
	path := shared.VarPath("shmounts")
	if filesystem.IsMountPoint(path) {
		daemon.SharedMountsSetup = true
		return nil
	}

	// Mount a new tmpfs
	err := unix.Mount("tmpfs", path, "tmpfs", 0, "size=100k,mode=0711")
	if err != nil {
		return err
	}

	// Mark as MS_SHARED and MS_REC
	var flags uintptr = unix.MS_SHARED | unix.MS_REC
	err = unix.Mount(path, path, "none", flags, "")
	if err != nil {
		return err
	}

	daemon.SharedMountsSetup = true
	return nil
}

// Init starts daemon process.
func (d *Daemon) Init() error {
	d.startTime = time.Now()

	return d.init()
}

func (d *Daemon) setupLoki(URL string, cert string, key string, caCert string, instanceName string, logLevel string, labels []string, types []string) error {
	// Stop any existing loki client.
	if d.lokiClient != nil {
		d.lokiClient.Stop()
	}

	// Check basic requirements for starting a new client.
	if URL == "" || logLevel == "" || len(types) == 0 {
		return nil
	}

	// Validate the URL.
	u, err := url.Parse(URL)
	if err != nil {
		return err
	}

	// Handle standalone systems.
	var location string
	if !d.serverClustered {
		hostname, err := os.Hostname()
		if err != nil {
			return err
		}

		location = hostname
		if instanceName == "" {
			instanceName = hostname
		}
	} else if instanceName == "" {
		instanceName = d.serverName
	}

	// Start a new client.
	d.lokiClient, err = loki.NewClient(d.shutdownCtx, u, cert, key, caCert, instanceName, location, logLevel, labels, types)
	if err != nil {
		return err
	}

	// Attach the new client to the log handler.
	d.internalListener.AddHandler("loki", d.lokiClient.HandleEvent)

	return nil
}

func (d *Daemon) init() error {
	d.startStopLock.Lock()
	defer d.startStopLock.Unlock()

	var dbWarnings []dbCluster.Warning

	var err error

	// Set default authorizer.
	d.authorizer, err = authDrivers.LoadAuthorizer(d.shutdownCtx, authDrivers.DriverTLS, logger.Log, d.identityCache)
	if err != nil {
		return err
	}

	// Setup events
	d.devLXDEvents = events.NewDevLXDServer(daemon.Debug, daemon.Verbose)
	d.events, err = events.NewServer(daemon.Debug, daemon.Verbose, cluster.EventHubPush)
	if err != nil {
		return err
	}

	// Configure logging events.
	events.LoggingServer = d.events

	// Setup internal event listener
	d.internalListener = events.NewInternalListener(d.shutdownCtx, d.events)

	// Lets check if there's an existing LXD running
	err = endpoints.CheckAlreadyRunning(d.os.GetUnixSocket())
	if err != nil {
		return err
	}

	/* Set the LVM environment */
	err = os.Setenv("LVM_SUPPRESS_FD_WARNINGS", "1")
	if err != nil {
		return err
	}

	/* Print welcome message */
	mode := "normal"
	if d.os.MockMode {
		mode = "mock"
	}

	logger.Info("LXD is starting", logger.Ctx{"version": version.Version, "mode": mode, "path": shared.VarPath("")})

	/* List of sub-systems to trace */
	trace := d.config.Trace

	/* Initialize the operating system facade */
	dbWarnings, err = d.os.Init()
	if err != nil {
		return err
	}

	// Setup AppArmor wrapper.
	rsync.RunWrapper = func(cmd *exec.Cmd, source string, destination string) (func(), error) {
		return apparmor.RsyncWrapper(d.os, cmd, source, destination)
	}

	// Bump some kernel limits to avoid issues
	for _, limit := range []int{unix.RLIMIT_NOFILE} {
		rLimit := unix.Rlimit{}
		err := unix.Getrlimit(limit, &rLimit)
		if err != nil {
			return err
		}

		rLimit.Cur = rLimit.Max

		err = unix.Setrlimit(limit, &rLimit)
		if err != nil {
			return err
		}
	}

	// Detect LXC features
	d.os.LXCFeatures = map[string]bool{}
	lxcExtensions := []string{
		"mount_injection_file",
		"seccomp_notify",
		"network_ipvlan",
		"network_l2proxy",
		"network_gateway_device_route",
		"network_phys_macvlan_mtu",
		"network_veth_router",
		"cgroup2",
		"pidfd",
		"seccomp_allow_deny_syntax",
		"devpts_fd",
		"seccomp_proxy_send_notify_fd",
		"idmapped_mounts_v2",
		"core_scheduling",
	}

	for _, extension := range lxcExtensions {
		d.os.LXCFeatures[extension] = liblxc.HasAPIExtension(extension)
	}

	// Look for kernel features
	logger.Info("Kernel features:")

	d.os.CloseRange = canUseCloseRange()
	if d.os.CloseRange {
		logger.Info(" - closing multiple file descriptors efficiently: yes")
	} else {
		logger.Info(" - closing multiple file descriptors efficiently: no")
	}

	d.os.NetnsGetifaddrs = canUseNetnsGetifaddrs()
	if d.os.NetnsGetifaddrs {
		logger.Info(" - netnsid-based network retrieval: yes")
	} else {
		logger.Info(" - netnsid-based network retrieval: no")
	}

	if canUsePidFds() && d.os.LXCFeatures["pidfd"] {
		d.os.PidFds = true
	}

	if d.os.PidFds {
		logger.Info(" - pidfds: yes")
	} else {
		logger.Info(" - pidfds: no")
	}

	if canUseCoreScheduling() {
		d.os.CoreScheduling = true
		logger.Info(" - core scheduling: yes")

		if d.os.LXCFeatures["core_scheduling"] {
			d.os.ContainerCoreScheduling = true
		}
	} else {
		logger.Info(" - core scheduling: no")
	}

	d.os.UeventInjection = canUseUeventInjection()
	if d.os.UeventInjection {
		logger.Info(" - uevent injection: yes")
	} else {
		logger.Info(" - uevent injection: no")
	}

	d.os.SeccompListener = canUseSeccompListener()
	if d.os.SeccompListener {
		logger.Info(" - seccomp listener: yes")
	} else {
		logger.Info(" - seccomp listener: no")
	}

	d.os.SeccompListenerContinue = canUseSeccompListenerContinue()
	if d.os.SeccompListenerContinue {
		logger.Info(" - seccomp listener continue syscalls: yes")
	} else {
		logger.Info(" - seccomp listener continue syscalls: no")
	}

	if canUseSeccompListenerAddfd() && d.os.LXCFeatures["seccomp_proxy_send_notify_fd"] {
		d.os.SeccompListenerAddfd = true
		logger.Info(" - seccomp listener add file descriptors: yes")
	} else {
		logger.Info(" - seccomp listener add file descriptors: no")
	}

	d.os.PidFdSetns = canUsePidFdSetns()
	if d.os.PidFdSetns {
		logger.Info(" - attach to namespaces via pidfds: yes")
	} else {
		logger.Info(" - attach to namespaces via pidfds: no")
	}

	if d.os.LXCFeatures["devpts_fd"] && canUseNativeTerminals() {
		d.os.NativeTerminals = true
		logger.Info(" - safe native terminal allocation: yes")
	} else {
		logger.Info(" - safe native terminal allocation: no")
	}

	d.os.UnprivBinfmt = canUseBinfmt()
	if d.os.UnprivBinfmt {
		logger.Info(" - unprivileged binfmt_misc: yes")
	} else {
		logger.Info(" - unprivileged binfmt_misc: no")
	}

	d.os.BPFToken = canUseBPFToken()
	if d.os.BPFToken {
		logger.Info(" - BPF Token: yes")
	} else {
		logger.Info(" - BPF Token: no")
	}

	/*
	 * During daemon startup we're the only thread that touches VFS3Fscaps
	 * so we don't need to bother with atomic.StoreInt32() when touching
	 * VFS3Fscaps.
	 */
	d.os.VFS3Fscaps = idmap.SupportsVFS3Fscaps("")
	if d.os.VFS3Fscaps {
		idmap.VFS3Fscaps = idmap.VFS3FscapsSupported
		logger.Info(" - unprivileged file capabilities: yes")
	} else {
		idmap.VFS3Fscaps = idmap.VFS3FscapsUnsupported
		logger.Info(" - unprivileged file capabilities: no")
	}

	dbWarnings = append(dbWarnings, d.os.CGInfo.Warnings()...)

	logger.Infof(" - cgroup layout: %s", d.os.CGInfo.Mode())

	for _, w := range dbWarnings {
		logger.Warnf(" - %s, %s", warningtype.TypeNames[warningtype.Type(w.TypeCode)], w.LastMessage)
	}

	// Detect idmapped mounts support.
	if shared.IsTrue(os.Getenv("LXD_IDMAPPED_MOUNTS_DISABLE")) {
		logger.Info(" - idmapped mounts kernel support: disabled")
	} else if kernelSupportsIdmappedMounts() {
		d.os.IdmappedMounts = true
		logger.Info(" - idmapped mounts kernel support: yes")
	} else {
		logger.Info(" - idmapped mounts kernel support: no")
	}

	// Detect and cached available instance types from operational drivers.
	drivers := instanceDrivers.DriverStatuses()
	for _, driver := range drivers {
		if driver.Warning != nil {
			dbWarnings = append(dbWarnings, *driver.Warning)
		}
	}

	// Validate the devices storage.
	testDev := shared.VarPath("devices", ".test")
	testDevNum := int(unix.Mkdev(0, 0))
	_ = os.Remove(testDev)
	err = unix.Mknod(testDev, 0600|unix.S_IFCHR, testDevNum)
	if err == nil {
		fd, err := os.Open(testDev)
		if err != nil && os.IsPermission(err) {
			logger.Warn("Unable to access device nodes, LXD likely running on a nodev mount")
			d.os.Nodev = true
		}

		_ = fd.Close()
		_ = os.Remove(testDev)
	}

	/* Initialize the database */
	err = initializeDbObject(d)
	if err != nil {
		return err
	}

	/* Setup network endpoint certificate */
	networkCert, err := util.LoadCert(d.os.VarDir)
	if err != nil {
		return err
	}

	/* Setup server certificate */
	serverCert, err := util.LoadServerCert(d.os.VarDir)
	if err != nil {
		return err
	}

	// Load cached local trusted certificates before starting listener and cluster database.
	err = updateIdentityCacheFromLocal(d)
	if err != nil {
		return err
	}

	d.serverClustered, err = cluster.Enabled(d.db.Node)
	if err != nil {
		return fmt.Errorf("Failed checking if clustered: %w", err)
	}

	// Detect if clustered, but not yet upgraded to per-server client certificates.
	if d.serverClustered && len(d.identityCache.GetByType(api.IdentityTypeCertificateServer)) < 1 {
		// If the cluster has not yet upgraded to per-server client certificates (by running patch
		// patchClusteringServerCertTrust) then temporarily use the network (cluster) certificate as client
		// certificate, and cause us to trust it for use as client certificate from the other members.
		networkCertFingerPrint := networkCert.Fingerprint()
		logger.Warn("No local trusted server certificates found, falling back to trusting network certificate", logger.Ctx{"fingerprint": networkCertFingerPrint})
		logger.Info("Set client certificate to network certificate", logger.Ctx{"fingerprint": networkCertFingerPrint})
		d.serverCertInt = networkCert
	} else {
		// If standalone or the local trusted certificates table is populated with server certificates then
		// use our local server certificate as client certificate for intra-cluster communication.
		logger.Info("Set client certificate to server certificate", logger.Ctx{"fingerprint": serverCert.Fingerprint()})
		d.serverCertInt = serverCert
	}

	// If we're clustered, check for an incoming recovery tarball
	if d.serverClustered {
		tarballPath := filepath.Join(d.db.Node.Dir(), cluster.RecoveryTarballName)

		if shared.PathExists(tarballPath) {
			err = cluster.DatabaseReplaceFromTarball(tarballPath, d.db.Node)
			if err != nil {
				return fmt.Errorf("Failed to load recovery tarball: %w", err)
			}
		}
	}

	// Load local config (must come after processing incoming recovery tarball as it can update local config).
	logger.Info("Loading daemon configuration")
	err = d.db.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		d.localConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}

	localHTTPAddress := d.localConfig.HTTPSAddress()
	localClusterAddress := d.localConfig.ClusterAddress()
	debugAddress := d.localConfig.DebugAddress()

	// Sense check for clustering mode.
	if localClusterAddress == "" && d.serverClustered {
		return errors.New("Server is clustered (has local raft addresses) but cluster.https_address is not set")
	}

	/* Setup dqlite */
	clusterLogLevel := "ERROR"
	if slices.Contains(trace, "dqlite") {
		clusterLogLevel = "TRACE"
	}

	d.gateway, err = cluster.NewGateway(
		d.shutdownCtx,
		d.db.Node,
		networkCert,
		d.State,
		cluster.Latency(d.config.RaftLatency),
		cluster.LogLevel(clusterLogLevel))
	if err != nil {
		return err
	}

	d.gateway.HeartbeatNodeHook = d.nodeRefreshTask

	/* Setup some mounts (nice to have) */
	if !d.os.MockMode {
		// Attempt to mount the shmounts tmpfs
		err := setupSharedMounts()
		if err != nil {
			logger.Warn("Failed setting up shared mounts", logger.Ctx{"err": err})
		}

		// Attempt to Mount the devLXD tmpfs
		devLXD := filepath.Join(d.os.VarDir, "devlxd")
		if !filesystem.IsMountPoint(devLXD) {
			err = unix.Mount("tmpfs", devLXD, "tmpfs", 0, "size=100k,mode=0755")
			if err != nil {
				logger.Warn("Failed to mount devLXD", logger.Ctx{"err": err})
			}
		}
	}

	if os.Getenv("LISTEN_PID") != "" {
		d.systemdSocketActivated = true
	}

	/* Setup the web server */
	endpointsConfig := &endpoints.Config{
		Dir:                  d.os.VarDir,
		UnixSocket:           d.os.GetUnixSocket(),
		Cert:                 networkCert,
		RestServer:           restServer(d),
		DevLxdServer:         devLXDServer(d),
		LocalUnixSocketGroup: d.config.Group,
		NetworkAddress:       localHTTPAddress,
		ClusterAddress:       localClusterAddress,
		DebugAddress:         debugAddress,
		MetricsServer:        metricsServer(d),
		StorageBucketsServer: storageBucketsServer(d),
		VsockServer:          vSockServer(d),
		VsockSupport:         false,
	}

	// Enable vsock server support if VM instances supported.
	err, found := d.State().InstanceTypes[instancetype.VM]
	if found && err == nil {
		endpointsConfig.VsockSupport = true
	}

	d.endpoints, err = endpoints.Up(endpointsConfig)
	if err != nil {
		return err
	}

	// Have the db package determine remote storage drivers
	db.StorageRemoteDriverNames = storageDrivers.RemoteDriverNames

	/* Open the cluster database */
	for {
		logger.Info("Initializing global database")
		dir := filepath.Join(d.os.VarDir, "database")

		store := d.gateway.NodeStore()

		contextTimeout := 30 * time.Second
		if !d.serverClustered {
			// FIXME: this is a workaround for #5234. We set a very
			// high timeout when we're not clustered, since there's
			// actually no networking involved.
			contextTimeout = time.Minute
		}

		options := []driver.Option{
			driver.WithDialFunc(d.gateway.DialFunc()),
			driver.WithContext(d.gateway.Context()),
			driver.WithConnectionTimeout(10 * time.Second),
			driver.WithContextTimeout(contextTimeout),
			driver.WithLogFunc(cluster.DqliteLog),
		}

		if slices.Contains(trace, "database") {
			options = append(options, driver.WithTracing(dqliteClient.LogDebug))
		}

		// Assign cluster DB handle to d.gateway.Cluster so its immediately usable by gateway even if DB
		// returns StatusPreconditionFailed. This way its usable for heartbeats whilst it waits for the
		// other members to become aligned.
		d.gateway.Cluster, err = db.OpenCluster(d.shutdownCtx, "db.bin", store, localClusterAddress, dir, d.config.DqliteSetupTimeout, nil, d.os.ServerUUID, options...)
		if err == nil {
			logger.Info("Initialized global database")

			// If cluster DB handle is established without issue, make available to the rest of LXD.
			d.db.Cluster = d.gateway.Cluster
			break
		} else if api.StatusErrorCheck(err, http.StatusPreconditionFailed) {
			// If some other nodes have schema or API versions less recent
			// than this node, we block until we receive a notification
			// from the last node being upgraded that everything should be
			// now fine, and then retry
			logger.Warn("Wait for other cluster members to align their versions, cluster not started yet")

			// The only thing we want to still do on this node is
			// to run the heartbeat task, in case we are the raft
			// leader.
			taskFunc, taskSchedule := cluster.HeartbeatTask(d.gateway)
			hbGroup := task.NewGroup()
			d.taskClusterHeartbeat = hbGroup.Add(taskFunc, taskSchedule)
			hbGroup.Start(d.shutdownCtx)

			{
				// Wait for refresh notification from other members.
				waitNotificationCtx, cancel := context.WithTimeout(d.shutdownCtx, time.Minute)
				d.gateway.WaitUpgradeNotification(waitNotificationCtx)
				cancel()
			}

			_ = hbGroup.Stop(time.Second)
			_ = d.gateway.Cluster.Close()

			d.gateway.HeartbeatLock.Lock()
			d.gateway.Cluster = nil
			d.gateway.HeartbeatLock.Unlock()

			continue
		}

		return fmt.Errorf("Failed to initialize global database: %w", err)
	}

	// Load the embedded OpenFGA authorizer. This cannot be loaded until after the cluster database is initialised,
	// so the TLS authorizer must be loaded first to set up clustering.
	d.authorizer, err = authDrivers.LoadAuthorizer(d.shutdownCtx, authDrivers.DriverEmbeddedOpenFGA, logger.Log, d.identityCache, authDrivers.WithOpenFGADatastore(openfga.NewOpenFGAStore(d.db.Cluster)))
	if err != nil {
		return err
	}

	d.firewall = firewall.New()
	logger.Info("Firewall loaded driver", logger.Ctx{"driver": d.firewall})

	err = cluster.NotifyUpgradeCompleted(d.State(), networkCert, d.serverCert())
	if err != nil {
		// Ignore the error, since it's not fatal for this particular
		// node. In most cases it just means that some nodes are
		// offline.
		logger.Warn("Could not notify all nodes of database upgrade", logger.Ctx{"err": err})
	}

	// Setup the user-agent.
	if d.serverClustered {
		version.UserAgentFeatures([]string{"cluster"})
	}

	// Apply all patches that need to be run before the cluster config gets loaded.
	err = patchesApply(d, patchPreLoadClusterConfig)
	if err != nil {
		return err
	}

	// Load server name and config before patches run (so they can access them from d.State()).
	err = d.db.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		globalConfig, err := clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Get the local node (will be used if clustered).
		serverName, err := tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		d.globalConfigMu.Lock()
		d.serverName = serverName
		d.globalConfig = globalConfig
		d.globalConfigMu.Unlock()

		// Add the per-project config options to the daemon config schema.
		projects, err := dbCluster.GetProjectNames(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed to get project names: %w", err)
		}

		for _, project := range projects {
			node.ConfigSchema["storage.project."+project+".images_volume"] = config.Key{}
			node.ConfigSchema["storage.project."+project+".backups_volume"] = config.Key{}
		}

		return nil
	})
	if err != nil {
		return err
	}

	d.events.SetLocalLocation(d.serverName)

	// Mount the storage pools.
	logger.Info("Initializing storage pools")
	err = storageStartup(d.State())
	if err != nil {
		return err
	}

	// Apply all patches that need to be run before daemon storage is initialised.
	err = patchesApply(d, patchPreDaemonStorage)
	if err != nil {
		return err
	}

	// Mount any daemon storage volumes.
	logger.Info("Initializing daemon storage mounts")
	err = daemonStorageMount(d.State())
	if err != nil {
		return err
	}

	// Create directories on daemon storage mounts.
	err = d.os.InitStorage(d.localConfig)
	if err != nil {
		return err
	}

	// Apply all patches that need to be run after daemon storage is initialised.
	err = patchesApply(d, patchPostDaemonStorage)
	if err != nil {
		return err
	}

	// Load server name and config after patches run (in case its been changed).
	err = d.db.Cluster.Transaction(d.shutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		config, err := clusterConfig.Load(ctx, tx)
		if err != nil {
			return err
		}

		// Get the local node (will be used if clustered).
		serverName, err := tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		d.globalConfigMu.Lock()
		d.serverName = serverName
		d.globalConfig = config
		d.globalConfigMu.Unlock()

		return nil
	})
	if err != nil {
		return err
	}

	d.events.SetLocalLocation(d.serverName)

	// Get daemon configuration.
	bgpAddress := d.localConfig.BGPAddress()
	bgpRouterID := d.localConfig.BGPRouterID()

	maasAPIURL := ""
	maasAPIKey := ""
	maasMachine := d.localConfig.MAASMachine()

	// Get specific config keys.
	d.globalConfigMu.Lock()
	bgpASN := d.globalConfig.BGPASN()

	d.proxy = shared.ProxyFromConfig(d.globalConfig.ProxyHTTPS(), d.globalConfig.ProxyHTTP(), d.globalConfig.ProxyIgnoreHosts())

	maasAPIURL, maasAPIKey = d.globalConfig.MAASController()
	d.gateway.HeartbeatOfflineThreshold = d.globalConfig.OfflineThreshold()
	lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiInstance, lokiLoglevel, lokiLabels, lokiTypes := d.globalConfig.LokiServer()
	oidcIssuer, oidcClientID, oidcClientSecret, oidcScopes, oidcAudience, oidcGroupsClaim := d.globalConfig.OIDCServer()
	syslogSocketEnabled := d.localConfig.SyslogSocket()

	d.endpoints.NetworkUpdateTrustedProxy(d.globalConfig.HTTPSTrustedProxy())
	d.globalConfigMu.Unlock()

	// Setup Loki logger.
	if lokiURL != "" {
		err = d.setupLoki(lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiInstance, lokiLoglevel, lokiLabels, lokiTypes)
		if err != nil {
			logger.Warn("Failed to setup Loki", logger.Ctx{"err": err})
		}
	}

	if syslogSocketEnabled {
		err = d.setupSyslogSocket(true)
		if err != nil {
			return err
		}
	}

	// Setup OIDC authentication.
	if oidcIssuer != "" && oidcClientID != "" {
		httpClientFunc := func() (*http.Client, error) {
			return util.HTTPClient("", d.proxy)
		}

		d.oidcVerifier, err = oidc.NewVerifier(oidcIssuer, oidcClientID, oidcClientSecret, oidcScopes, oidcAudience, d.getCoreAuthSecrets, d.identityCache, httpClientFunc, &oidc.Opts{GroupsClaim: oidcGroupsClaim})
		if err != nil {
			return err
		}
	}

	// Setup BGP listener.
	d.bgp = bgp.NewServer()

	// Setup DNS listener.
	d.dns = dns.NewServer(d.db.Cluster, func(name string, full bool) (*dns.Zone, error) {
		// Fetch the zone.
		zone, err := networkZone.LoadByName(d.State(), name)
		if err != nil {
			return nil, err
		}

		zoneInfo := zone.Info()

		// Fill in the zone information.
		resp := &dns.Zone{}
		resp.Info = *zoneInfo

		if full {
			// Full content was requested.
			zoneBuilder, err := zone.Content()
			if err != nil {
				logger.Errorf("Failed to render DNS zone %q: %v", name, err)
				return nil, err
			}

			resp.Content = strings.TrimSpace(zoneBuilder.String())
		} else {
			// SOA only.
			zoneBuilder, err := zone.SOA()
			if err != nil {
				logger.Errorf("Failed to render DNS zone %q: %v", name, err)
				return nil, err
			}

			resp.Content = strings.TrimSpace(zoneBuilder.String())
		}

		return resp, nil
	})

	// Setup the networks.
	logger.Info("Initializing networks")

	err = networkStartup(d.State, false)
	if err != nil {
		return err
	}

	// Setup tertiary listeners that may use managed network addresses and must be started after networks.
	if bgpAddress != "" && bgpASN != 0 && bgpRouterID != "" {
		if bgpASN > math.MaxUint32 {
			return errors.New("Cannot convert BGP ASN to uint32: Upper bound exceeded")
		}

		err := d.bgp.Configure(bgpAddress, uint32(bgpASN), net.ParseIP(bgpRouterID))
		if err != nil {
			return err
		}

		logger.Info("Started BGP server")
	}

	dnsAddress := d.localConfig.DNSAddress()
	if dnsAddress != "" {
		err = d.dns.Start(dnsAddress)
		if err != nil {
			return err
		}

		logger.Info("Started DNS server")
	}

	metricsAddress := d.localConfig.MetricsAddress()
	if metricsAddress != "" {
		err = d.endpoints.UpMetrics(metricsAddress)
		if err != nil {
			return err
		}
	}

	storageBucketsAddress := d.localConfig.StorageBucketsAddress()
	if storageBucketsAddress != "" {
		err = d.endpoints.UpStorageBuckets(storageBucketsAddress)
		if err != nil {
			return err
		}
	}

	// Apply all patches that need to be run after networks are initialised.
	err = patchesApply(d, patchPostNetworks)
	if err != nil {
		return err
	}

	// Cleanup leftover images.
	pruneLeftoverImages(d.State())

	var instances []instance.Instance

	if !d.os.MockMode {
		// Start the scheduler
		go deviceEventListener(d.State)

		prefixPath := os.Getenv("LXD_DEVMONITOR_DIR")
		if prefixPath == "" {
			prefixPath = "/dev"
		}

		logger.Info("Starting device monitor")

		d.devmonitor, err = fsmonitorDrivers.Load(d.State().ShutdownCtx, prefixPath, fsmonitor.EventAdd, fsmonitor.EventRemove)
		if err != nil {
			return err
		}

		// Must occur after d.devmonitor has been initialised.
		instances, err = instance.LoadNodeAll(d.State(), instancetype.Any)
		if err != nil {
			return fmt.Errorf("Failed loading local instances: %w", err)
		}

		// Register devices on running instances to receive events and reconnect to VM monitor sockets.
		// This should come after the event handler go routines have been started.
		devicesRegister(instances)

		// Setup seccomp handler
		if d.os.SeccompListener {
			seccompServer, err := seccomp.NewSeccompServer(d.State(), shared.VarPath("seccomp.socket"), func(pid int32, state *state.State) (seccomp.Instance, error) {
				c, _, err := getLXCMonitorContainer(state, pid)
				if err != nil || c == nil {
					logger.Warn("Could not match PID to container for seccomp", logger.Ctx{"pid": pid, "err": err})
					return nil, errPIDNotInContainer // Don't return error to avoid leaking details about the process.
				}

				return c, nil
			})
			if err != nil {
				return err
			}

			d.seccomp = seccompServer
			logger.Info("Started seccomp handler", logger.Ctx{"path": shared.VarPath("seccomp.socket")})
		}

		// Read the trusted identities
		updateIdentityCache(d)

		// Connect to MAAS
		if maasAPIURL != "" {
			go func() {
				warningAdded := false

				for {
					err = d.setupMAASController(maasAPIURL, maasAPIKey, maasMachine)
					if err == nil {
						logger.Info("Connected to MAAS controller", logger.Ctx{"url": maasAPIURL})
						break
					}

					logger.Warn("Unable to connect to MAAS, trying again in a minute", logger.Ctx{"url": maasAPIURL, "err": err})

					if !warningAdded {
						_ = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
							err := tx.UpsertWarningLocalNode(ctx, "", "", -1, warningtype.UnableToConnectToMAAS, err.Error())
							if err != nil {
								logger.Warn("Failed to create warning", logger.Ctx{"err": err})
							}

							return nil
						})

						warningAdded = true
					}

					time.Sleep(time.Minute)
				}

				// Resolve any previously created warning once connected
				if warningAdded {
					_ = warnings.ResolveWarningsByLocalNodeAndType(d.db.Cluster, warningtype.UnableToConnectToMAAS)
				}
			}()
		}
	}

	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Remove volatile.last_state.ready key as we don't know if the instances are ready.
		return tx.DeleteReadyStateFromLocalInstances(ctx)
	})
	if err != nil {
		return fmt.Errorf("Failed deleting volatile.last_state.ready: %w", err)
	}

	close(d.setupChan)

	_ = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create warnings that have been collected
		for _, w := range dbWarnings {
			err := tx.UpsertWarningLocalNode(ctx, "", "", -1, warningtype.Type(w.TypeCode), w.LastMessage)
			if err != nil {
				logger.Warn("Failed to create warning", logger.Ctx{"err": err})
			}
		}

		return nil
	})

	// Resolve warnings older than the daemon start time
	err = warnings.ResolveWarningsByLocalNodeOlderThan(d.db.Cluster, d.startTime)
	if err != nil {
		logger.Warn("Failed to resolve warnings", logger.Ctx{"err": err})
	}

	// Start cluster tasks if needed.
	if d.serverClustered {
		d.startClusterTasks()
	}

	d.tasks = task.NewGroup()

	// FIXME: There's no hard reason for which we should not run these
	//        tasks in mock mode. However it requires that we tweak them so
	//        they exit gracefully without blocking (something we should do
	//        anyways) and they don't hit the internet or similar. Support
	//        for proper cancellation is something that has been started
	//        but has not been fully completed.
	if !d.os.MockMode {
		// Log expiry (daily)
		d.tasks.Add(expireLogsTask(d.State))

		// Remove expired images (daily)
		d.taskPruneImages = d.tasks.Add(pruneExpiredImagesTask(d.State))

		// Auto-update images (every 6 hours, configurable)
		d.tasks.Add(autoUpdateImagesTask(d.State))

		// Auto-update instance types (daily)
		d.tasks.Add(instanceRefreshTypesTask(d.State))

		// Remove expired backups (hourly)
		d.tasks.Add(pruneExpiredBackupsTask(d.State))

		// Prune expired instance snapshots and take snapshot of instances (minutely check of configurable cron expression)
		d.tasks.Add(pruneExpiredAndAutoCreateInstanceSnapshotsTask(d.State))

		// Prune expired custom volume snapshots and take snapshots of custom volumes (minutely check of configurable cron expression)
		d.tasks.Add(pruneExpiredAndAutoCreateCustomVolumeSnapshotsTask(d.State))

		// Remove resolved warnings (daily)
		d.tasks.Add(pruneResolvedWarningsTask(d.State))

		// Auto-renew server certificate (daily)
		d.tasks.Add(autoRenewCertificateTask(d))

		// Remove expired tokens (hourly)
		d.tasks.Add(autoRemoveExpiredTokensTask(d.State))
	}

	// Start all background tasks
	d.tasks.Start(d.shutdownCtx)

	// Load Ubuntu Pro configuration before starting any instances.
	d.ubuntuPro = ubuntupro.New(d.shutdownCtx, d.os.ReleaseInfo["NAME"])

	// Restore instances
	instancesStart(d.State(), instances)

	// Re-balance in case things changed while LXD was down
	deviceTaskBalance(d.State())

	// Unblock incoming requests
	d.waitReady.Cancel()

	logger.Info("Daemon started")

	return nil
}

func (d *Daemon) startClusterTasks() {
	// Add initial event listeners from global database members.
	// Run asynchronously so that connecting to remote members doesn't delay starting up other cluster tasks.
	go cluster.EventsUpdateListeners(d.endpoints, d.db.Cluster, d.serverCert, nil, d.events.Inject)

	d.clusterTasks = task.NewGroup()

	// Heartbeats
	d.taskClusterHeartbeat = d.clusterTasks.Add(cluster.HeartbeatTask(d.gateway))

	// Auto-sync images across the cluster (hourly)
	d.clusterTasks.Add(autoSyncImagesTask(d.State))

	// Remove orphaned operations
	d.clusterTasks.Add(autoRemoveOrphanedOperationsTask(d.State))

	// Perform automatic evacuation for offline cluster members
	d.clusterTasks.Add(autoHealClusterTask(d.State))

	// Start all background tasks
	d.clusterTasks.Start(d.shutdownCtx)
}

func (d *Daemon) stopClusterTasks() {
	err := d.clusterTasks.Stop(3 * time.Second)
	if err != nil {
		logger.Warn("Failed stopping cluster tasks", logger.Ctx{"err": err})
	}

	d.clusterTasks = task.NewGroup()
}

// numRunningInstances returns the number of running instances.
func (d *Daemon) numRunningInstances(instances []instance.Instance) int {
	count := 0
	for _, instance := range instances {
		if instance.IsRunning() {
			count = count + 1
		}
	}

	return count
}

// cancelCancelableOps cancels all running cancelable operations.
func cancelCancelableOps() error {
	ops := operations.Clone()
	for _, op := range ops {
		if op.Status() != api.Running || op.Class() == operations.OperationClassToken {
			continue
		}

		_, opAPI, err := op.Render()
		if err != nil {
			return fmt.Errorf("Failed to render an operation while attempting to stop cancelable operations: %w", err)
		}

		if opAPI.MayCancel {
			_, _ = op.Cancel()
		}
	}

	return nil
}

// Stop stops the shared daemon.
func (d *Daemon) Stop(ctx context.Context, sig os.Signal) error {
	// Cancelling the context will make everyone aware that we're shutting down.
	d.shutdownCtx.Cancel()

	d.startStopLock.Lock()
	defer d.startStopLock.Unlock()

	logger.Info("Starting shutdown sequence", logger.Ctx{"signal": sig})

	s := d.State()

	if d.gateway != nil {
		d.stopClusterTasks()

		err := handoverMemberRole(d.State(), d.gateway)
		if err != nil {
			logger.Warn("Could not handover member's responsibilities", logger.Ctx{"err": err})
			d.gateway.Kill()
		}
	}

	// Stop any running minio processes cleanly before unmount storage pools.
	miniod.StopAll()

	var err error
	var instances []instance.Instance
	var instancesLoaded bool // If this is left as false this indicates an error loading instances.

	if d.db.Cluster != nil {
		instances, err = instance.LoadNodeAll(s, instancetype.Any)
		if err != nil {
			// List all instances on disk.
			logger.Warn("Loading local instances from disk as database is not available", logger.Ctx{"err": err})
			instances, err = instancesOnDisk(s)
			if err != nil {
				logger.Warn("Failed loading instances from disk", logger.Ctx{"err": err})
			}

			// Make all future queries fail fast as DB is not available.
			d.gateway.Kill()
			_ = d.db.Cluster.Close()
		}

		if err == nil {
			instancesLoaded = true
		}
	}

	// Handle shutdown (unix.SIGPWR) and reload (unix.SIGTERM) signals.
	if sig == unix.SIGPWR || sig == unix.SIGTERM {
		// Full shutdown requested.
		if sig == unix.SIGPWR {
			{
				logger.Debug("Shutting down instances")
				var instOperationWaitCtx context.Context
				var cancel context.CancelFunc
				if s.GlobalConfig != nil {
					instOperationWaitCtx, cancel = context.WithTimeout(ctx, s.GlobalConfig.ShutdownTimeout())
					defer cancel()
				} else {
					instOperationWaitCtx, cancel = context.WithCancel(ctx)
					cancel() // Don't wait for operations to finish.
				}

				instancesShutdown(instOperationWaitCtx, instances)
			}

			if d.db.Cluster != nil {
				// Try to cancel any cancelable operations.
				err = cancelCancelableOps()
				if err != nil {
					logger.Error("Failed to cancel cancelable operations", logger.Ctx{"err": err})
				}
			}

			// Stop networks.
			networkStop(s, false)

			// Unmount daemon image and backup volumes if set.
			logger.Info("Stopping daemon storage volumes")
			logger.Debug("Unmounting daemon storage volumes")
			volUnmountCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			err := daemonStorageVolumesUnmount(s, volUnmountCtx)
			if err != nil {
				logger.Error("Failed to unmount image and backup volumes", logger.Ctx{"err": err})
			}

			logger.Debug("Daemon storage volumes unmounted")

			// Unmount storage pools after instances stopped and images/backup volumes unmounted.
			storageStop(s)
		}

		if d.db.Cluster != nil {
			// Remove remaining operations before closing the database.
			err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				err := dbCluster.DeleteOperations(ctx, tx.Tx(), s.DB.Cluster.GetNodeID())
				if err != nil {
					logger.Error("Failed cleaning up operations")
				}

				return nil
			})
			if err != nil {
				logger.Error("Failed cleaning up operations", logger.Ctx{"err": err})
			} else {
				logger.Debug("Operations deleted from the database")
			}
		}
	}

	if d.gateway != nil {
		d.gateway.Kill()
	}

	errs := []error{}
	trackError := func(err error, desc string) {
		if err != nil {
			errs = append(errs, fmt.Errorf(desc+": %w", err))
		}
	}

	trackError(d.tasks.Stop(3*time.Second), "Stop tasks")                // Give tasks a bit of time to cleanup.
	trackError(d.clusterTasks.Stop(3*time.Second), "Stop cluster tasks") // Give tasks a bit of time to cleanup.

	n := d.numRunningInstances(instances)
	shouldUnmount := instancesLoaded && n <= 0

	if d.db.Cluster != nil {
		logger.Info("Closing the database")
		err := d.db.Cluster.Close()
		if err != nil {
			logger.Warn("Could not close global database cleanly", logger.Ctx{"err": err})
		}
	}

	if d.db != nil && d.db.Node != nil {
		trackError(d.db.Node.Close(), "Close local database")
	}

	if d.gateway != nil {
		trackError(d.gateway.Shutdown(), "Shutdown dqlite")
	}

	if d.endpoints != nil {
		trackError(d.endpoints.Down(), "Shutdown endpoints")
	}

	if shouldUnmount {
		logger.Info("Unmounting temporary filesystems")

		_ = unix.Unmount(shared.VarPath("devlxd"), unix.MNT_DETACH)
		_ = unix.Unmount(shared.VarPath("shmounts"), unix.MNT_DETACH)

		logger.Info("Done unmounting temporary filesystems")
	} else {
		logger.Info("Not unmounting temporary filesystems (instances are still running)")
	}

	if d.seccomp != nil {
		trackError(d.seccomp.Stop(), "Stop seccomp")
	}

	trackError(filesystem.SyncFS(filepath.Join(d.os.VarDir, "database")), "Sync database directory")

	n = len(errs)
	if n > 0 {
		format := "%v"
		if n > 1 {
			format += fmt.Sprint(" (and ", n, " more errors)")
		}

		err = fmt.Errorf(format, errs[0])
	}

	if err != nil {
		logger.Error("Failed to cleanly shutdown daemon", logger.Ctx{"err": err})
	}

	return err
}

// Setup MAAS.
func (d *Daemon) setupMAASController(server string, key string, machine string) error {
	var err error
	d.maas = nil

	// Default the machine name to the hostname
	if machine == "" {
		machine, err = os.Hostname()
		if err != nil {
			return err
		}
	}

	// We need both URL and key, otherwise disable MAAS
	if server == "" || key == "" {
		return nil
	}

	// Get a new controller struct
	controller, err := maas.NewController(server, key, machine)
	if err != nil {
		d.maas = nil
		return err
	}

	d.maas = controller
	return nil
}

func (d *Daemon) setupSyslogSocket(enable bool) error {
	// Always cancel the context to ensure that no goroutines leak.
	if d.syslogSocketCancel != nil {
		logger.Debug("Stopping syslog socket")
		d.syslogSocketCancel()
	}

	if !enable {
		return nil
	}

	var ctx context.Context

	ctx, d.syslogSocketCancel = context.WithCancel(d.shutdownCtx)

	logger.Debug("Starting syslog socket")

	err := StartSyslogListener(ctx, d.events)
	if err != nil {
		return err
	}

	return nil
}

// Create a database connection and perform any updates needed.
func initializeDbObject(d *Daemon) error {
	logger.Info("Initializing local database")

	// Hook to run when the local database is created from scratch. It will
	// create the default profile and mark all patches as applied.
	freshHook := func(db *db.Node) error {
		for _, patchName := range patchesGetNames() {
			err := db.MarkPatchAsApplied(patchName)
			if err != nil {
				return err
			}
		}
		return nil
	}

	var err error
	d.db.Node, err = db.OpenNode(filepath.Join(d.os.VarDir, "database"), freshHook)
	if err != nil {
		return fmt.Errorf("Error creating database: %s", err)
	}

	return nil
}

// hasMemberStateChanged returns true if the number of members, their addresses or state has changed.
func (d *Daemon) hasMemberStateChanged(heartbeatData *cluster.APIHeartbeat) bool {
	// No previous heartbeat data.
	if d.lastNodeList == nil {
		return true
	}

	// Member count has changed.
	if len(d.lastNodeList.Members) != len(heartbeatData.Members) {
		return true
	}

	// Check for member address or state changes.
	for lastMemberID, lastMember := range d.lastNodeList.Members {
		if heartbeatData.Members[lastMemberID].Address != lastMember.Address {
			return true
		}

		if heartbeatData.Members[lastMemberID].Online != lastMember.Online {
			return true
		}
	}

	return false
}

// heartbeatHandler handles heartbeat requests from other cluster members.
func (d *Daemon) heartbeatHandler(w http.ResponseWriter, r *http.Request, isLeader bool, hbData *cluster.APIHeartbeat) {
	s := d.State()

	var err error

	// Look for time skews.
	now := time.Now().UTC()

	if hbData.Time.Add(5*time.Second).Before(now) || hbData.Time.Add(-5*time.Second).After(now) {
		if !d.timeSkew {
			logger.Warn("Time skew detected between leader and local", logger.Ctx{"leaderTime": hbData.Time, "localTime": now})

			if d.db.Cluster != nil {
				err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.UpsertWarningLocalNode(ctx, "", "", -1, warningtype.ClusterTimeSkew, fmt.Sprintf("leaderTime: %s, localTime: %s", hbData.Time, now))
				})
				if err != nil {
					logger.Warn("Failed to create cluster time skew warning", logger.Ctx{"err": err})
				}
			}
		}

		d.timeSkew = true
	} else {
		if d.timeSkew {
			logger.Warn("Time skew resolved")

			if d.db.Cluster != nil {
				err := warnings.ResolveWarningsByLocalNodeAndType(d.db.Cluster, warningtype.ClusterTimeSkew)
				if err != nil {
					logger.Warn("Failed to resolve cluster time skew warning", logger.Ctx{"err": err})
				}
			}

			d.timeSkew = false
		}
	}

	// Extract the raft nodes from the heartbeat info.
	raftNodes := make([]db.RaftNode, 0)
	for _, node := range hbData.Members {
		if node.RaftID > 0 {
			raftNodes = append(raftNodes, db.RaftNode{
				NodeInfo: dqliteClient.NodeInfo{
					ID:      node.RaftID,
					Address: node.Address,
					Role:    db.RaftRole(node.RaftRole),
				},
				Name: node.Name,
			})
		}
	}

	// Check we have been sent at least 1 raft node before wiping our set.
	if len(raftNodes) <= 0 {
		logger.Error("Empty raft member set received")
		http.Error(w, "400 Empty raft member set received", http.StatusBadRequest)
		return
	}

	// Accept raft node list from any heartbeat type so that we get freshest data quickly.
	logger.Debug("Replace current raft nodes", logger.Ctx{"raftMembers": raftNodes})
	err = d.db.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceRaftNodes(raftNodes)
	})
	if err != nil {
		logger.Error("Error updating raft members", logger.Ctx{"err": err})
		http.Error(w, "500 failed to update raft nodes", http.StatusInternalServerError)
		return
	}

	localClusterAddress := s.LocalConfig.ClusterAddress()

	if hbData.FullStateList {
		// If there is an ongoing heartbeat round (and by implication this is the leader), then this could
		// be a problem because it could be broadcasting the stale member state information which in turn
		// could lead to incorrect decisions being made. So calling heartbeatRestart will request any
		// ongoing heartbeat round to cancel itself prematurely and restart another one. If there is no
		// ongoing heartbeat round or this member isn't the leader then this function call is a no-op and
		// will return false. If the heartbeat is restarted, then the heartbeat refresh task will be called
		// at the end of the heartbeat so no need to do it here.
		if !isLeader || !d.gateway.HeartbeatRestart() {
			// Run heartbeat refresh task async so heartbeat response is sent to leader straight away.
			go d.nodeRefreshTask(hbData, isLeader, nil)
		}
	} else {
		if isLeader {
			logger.Error("Partial heartbeat should not be sent to leader")
			http.Error(w, "400 Partial heartbeat should not be sent to leader", http.StatusBadRequest)
			return
		}

		logger.Info("Partial heartbeat received", logger.Ctx{"local": localClusterAddress})
	}
}

// nodeRefreshTask is run when a full state heartbeat is sent (on the leader) or received (by a non-leader member).
// Is is used to check for member state changes and trigger refreshes of the certificate cache and forkdns peers.
// It also triggers member role promotion when run on the isLeader is true.
// When run on the leader, it accepts a list of unavailableMembers that have not responded to the current heartbeat
// round (but may not be considered actually offline at this stage). These unavailable members will not be used for
// role rebalancing.
func (d *Daemon) nodeRefreshTask(heartbeatData *cluster.APIHeartbeat, isLeader bool, unavailableMembers []string) {
	s := d.State()

	// Don't process the heartbeat until we're fully online.
	if d.db.Cluster == nil || d.db.Cluster.GetNodeID() == 0 {
		return
	}

	localClusterAddress := s.LocalConfig.ClusterAddress()

	if !heartbeatData.FullStateList || len(heartbeatData.Members) <= 0 {
		logger.Error("Heartbeat member refresh task called with partial state list", logger.Ctx{"local": localClusterAddress})
		return
	}

	// If the max version of the cluster has changed, check whether we need to upgrade.
	if d.lastNodeList == nil || d.lastNodeList.Version.APIExtensions != heartbeatData.Version.APIExtensions || d.lastNodeList.Version.Schema != heartbeatData.Version.Schema {
		err := cluster.MaybeUpdate(s)
		if err != nil {
			logger.Error("Error updating", logger.Ctx{"err": err})
			return
		}
	}

	stateChangeTaskFailure := false // Records whether any of the state change tasks failed.

	// Handle potential OVN chassis changes.
	err := networkUpdateOVNChassis(s, heartbeatData, localClusterAddress)
	if err != nil {
		stateChangeTaskFailure = true
		logger.Error("Error restarting OVN networks", logger.Ctx{"err": err})
	}

	if d.hasMemberStateChanged(heartbeatData) {
		logger.Info("Cluster member state has changed", logger.Ctx{"local": localClusterAddress})

		// Refresh the identity cache.
		updateIdentityCache(d)

		// Refresh forkdns peers.
		err := networkUpdateForkdnsServersTask(s, heartbeatData)
		if err != nil {
			stateChangeTaskFailure = true
			logger.Error("Error refreshing forkdns", logger.Ctx{"err": err, "local": localClusterAddress})
		}
	}

	// Refresh event listeners from heartbeat members (after certificates refreshed if needed).
	// Run asynchronously so that connecting to remote members doesn't delay other heartbeat tasks.
	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		cluster.EventsUpdateListeners(d.endpoints, d.db.Cluster, d.serverCert, heartbeatData.Members, d.events.Inject)
		wg.Done()
	}()

	// Only update the node list if there are no state change task failures.
	// If there are failures, then we leave the old state so that we can re-try the tasks again next heartbeat.
	if !stateChangeTaskFailure {
		d.lastNodeList = heartbeatData
	}

	// If we are leader and called from the leader heartbeat send function (unavailbleMembers != nil) and there
	// are other members in the cluster, then check if we need to update roles. We do not want to do this if
	// we are called on the leader as part of a notification heartbeat being received from another member.
	if isLeader && unavailableMembers != nil && len(heartbeatData.Members) > 1 {
		isDegraded := false
		hasNodesNotPartOfRaft := false
		onlineVoters := int64(0)
		onlineStandbys := int64(0)

		for _, node := range heartbeatData.Members {
			role := db.RaftRole(node.RaftRole)
			if node.Online {
				// Count online members that have voter or stand-by raft role.
				switch role {
				case db.RaftVoter:
					onlineVoters++
				case db.RaftStandBy:
					onlineStandbys++
				}

				if node.RaftID == 0 {
					hasNodesNotPartOfRaft = true
				}
			} else if role != db.RaftSpare {
				isDegraded = true // Offline member that has voter or stand-by raft role.
			}
		}

		maxVoters := s.GlobalConfig.MaxVoters()
		maxStandBy := s.GlobalConfig.MaxStandBy()

		// If there are offline members that have voter or stand-by database roles, let's see if we can
		// replace them with spare ones. Also, if we don't have enough voters or standbys, let's see if we
		// can upgrade some member.
		if isDegraded || onlineVoters < maxVoters || onlineStandbys < maxStandBy {
			d.clusterMembershipMutex.Lock()
			logger.Debug("Rebalancing member roles in heartbeat", logger.Ctx{"local": localClusterAddress})
			err := rebalanceMemberRoles(context.Background(), d.State(), d.gateway, unavailableMembers)
			if err != nil && !errors.Is(err, cluster.ErrNotLeader) {
				logger.Warn("Could not rebalance cluster member roles", logger.Ctx{"err": err, "local": localClusterAddress})
			}

			d.clusterMembershipMutex.Unlock()
		}

		if hasNodesNotPartOfRaft {
			d.clusterMembershipMutex.Lock()
			logger.Debug("Upgrading members without raft role in heartbeat", logger.Ctx{"local": localClusterAddress})
			err := upgradeNodesWithoutRaftRole(d.State(), d.gateway)
			if err != nil && !errors.Is(err, cluster.ErrNotLeader) {
				logger.Warn("Failed upgrading raft roles:", logger.Ctx{"err": err, "local": localClusterAddress})
			}

			d.clusterMembershipMutex.Unlock()
		}
	}

	wg.Wait()
}
