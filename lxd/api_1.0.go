package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/auth/oidc"
	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/cluster"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/config"
	"github.com/canonical/lxd/lxd/db"
	instanceDrivers "github.com/canonical/lxd/lxd/instance/drivers"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	scriptletLoad "github.com/canonical/lxd/lxd/scriptlet/load"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/version"
)

var api10Cmd = APIEndpoint{
	MetricsType: entity.TypeServer,

	Get:   APIEndpointAction{Handler: api10Get, AllowUntrusted: true},
	Patch: APIEndpointAction{Handler: api10Patch, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
	Put:   APIEndpointAction{Handler: api10Put, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var api10 = []APIEndpoint{
	api10Cmd,
	api10ResourcesCmd,
	certificateCmd,
	certificatesCmd,
	clusterCmd,
	clusterGroupCmd,
	clusterGroupsCmd,
	clusterNodeCmd,
	clusterNodeStateCmd,
	clusterNodesCmd,
	clusterCertificateCmd,
	instanceBackupCmd,
	instanceBackupExportCmd,
	instanceBackupsCmd,
	instanceCmd,
	instanceConsoleCmd,
	instanceExecCmd,
	instanceFileCmd,
	instanceExecOutputCmd,
	instanceExecOutputsCmd,
	instanceLogCmd,
	instanceLogsCmd,
	instanceMetadataCmd,
	instanceMetadataTemplatesCmd,
	instancesCmd,
	instanceRebuildCmd,
	instanceSFTPCmd,
	instanceSnapshotCmd,
	instanceSnapshotsCmd,
	instanceStateCmd,
	instanceUEFIVarsCmd,
	eventsCmd,
	imageAliasCmd,
	imageAliasesCmd,
	imageCmd,
	imageExportCmd,
	imageRefreshCmd,
	imagesCmd,
	imageSecretCmd,
	metadataConfigurationCmd,
	networkCmd,
	networkLeasesCmd,
	networksCmd,
	networkStateCmd,
	networkACLCmd,
	networkACLsCmd,
	networkACLLogCmd,
	networkAllocationsCmd,
	networkForwardCmd,
	networkForwardsCmd,
	networkLoadBalancerCmd,
	networkLoadBalancersCmd,
	networkPeerCmd,
	networkPeersCmd,
	networkZoneCmd,
	networkZonesCmd,
	networkZoneRecordCmd,
	networkZoneRecordsCmd,
	operationCmd,
	operationsCmd,
	operationWait,
	operationWebsocket,
	profileCmd,
	profilesCmd,
	projectCmd,
	projectsCmd,
	projectStateCmd,
	storagePoolCmd,
	storagePoolResourcesCmd,
	storagePoolsCmd,
	storagePoolBucketsCmd,
	storagePoolBucketCmd,
	storagePoolBucketKeysCmd,
	storagePoolBucketKeyCmd,
	storagePoolVolumesCmd,
	storagePoolVolumeSnapshotsTypeCmd,
	storagePoolVolumeSnapshotTypeCmd,
	storagePoolVolumesTypeCmd,
	storagePoolVolumeTypeCmd,
	storagePoolVolumeTypeCustomBackupsCmd,
	storagePoolVolumeTypeCustomBackupCmd,
	storagePoolVolumeTypeCustomBackupExportCmd,
	storagePoolVolumeTypeStateCmd,
	warningsCmd,
	warningCmd,
	metricsCmd,
	identitiesCmd,
	currentIdentityCmd,
	tlsIdentityCmd,
	oidcIdentityCmd,
	tlsIdentitiesCmd,
	oidcIdentitiesCmd,
	authGroupsCmd,
	authGroupCmd,
	identityProviderGroupsCmd,
	identityProviderGroupCmd,
	permissionsCmd,
	storageVolumesCmd,
	storageVolumesTypeCmd,
}

// swagger:operation GET /1.0?public server server_get_untrusted
//
//  Get the server environment
//
//  Shows a small subset of the server environment and configuration
//  which is required by untrusted clients to reach a server.
//
//  The `?public` part of the URL isn't required, it's simply used to
//  separate the two behaviors of this endpoint.
//
//  ---
//  produces:
//    - application/json
//  responses:
//    "200":
//      description: Server environment and configuration
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            $ref: "#/definitions/ServerUntrusted"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0 server server_get
//
//	Get the server environment and configuration
//
//	Shows the full server environment and configuration.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Server environment and configuration
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Server"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func api10Get(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the authentication methods.
	authMethods := []string{api.AuthenticationMethodTLS}

	oidcIssuer, oidcClientID, _, _, _, _ := s.GlobalConfig.OIDCServer()
	if oidcIssuer != "" && oidcClientID != "" {
		authMethods = append(authMethods, api.AuthenticationMethodOIDC)
	}

	srv := api.ServerUntrusted{
		APIExtensions:     version.APIExtensions,
		APIStatus:         "stable",
		APIVersion:        version.APIVersion,
		Public:            false,
		Auth:              api.AuthUntrusted,
		AuthMethods:       authMethods,
		ClientCertificate: r.TLS != nil && len(r.TLS.PeerCertificates) > 0,
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	// If not authenticated, return now.
	if !requestor.IsTrusted() {
		daemonConfig, _ := daemonConfigRender(s)
		_, flagExists := daemonConfig["user.microcloud"]
		if flagExists {
			// Unprivileged users may see the user.microcloud config key
			srv.Config = map[string]any{
				"user.microcloud": daemonConfig["user.microcloud"],
			}
		}

		return response.SyncResponseETag(true, srv, nil)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeServer, false)
	if err != nil {
		return response.SmartError(err)
	}

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	srv.Auth = api.AuthTrusted

	localHTTPSAddress := s.LocalConfig.HTTPSAddress()

	addresses, err := util.ListenAddresses(localHTTPSAddress)
	if err != nil {
		return response.InternalError(err)
	}

	// When clustered, use the node name, otherwise use the hostname.
	var serverName string
	if s.ServerClustered {
		serverName = s.ServerName
	} else {
		hostname, err := os.Hostname()
		if err != nil {
			return response.SmartError(err)
		}

		serverName = hostname
	}

	certificate := string(s.Endpoints.NetworkPublicKey())
	var certificateFingerprint string
	if certificate != "" {
		certificateFingerprint, err = shared.CertFingerprintStr(certificate)
		if err != nil {
			return response.InternalError(err)
		}
	}

	architectures := []string{}

	for _, architecture := range s.OS.Architectures {
		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			return response.InternalError(err)
		}

		architectures = append(architectures, architectureName)
	}

	projectName := r.FormValue("project")
	if projectName == "" {
		projectName = api.ProjectDefaultName
	}

	env := api.ServerEnvironment{
		Addresses:                  addresses,
		Architectures:              architectures,
		BackupMetadataVersionRange: []uint32{api.BackupMetadataVersion1, backupConfig.MaxMetadataVersion},
		Certificate:                certificate,
		CertificateFingerprint:     certificateFingerprint,
		Kernel:                     s.OS.Uname.Sysname,
		KernelArchitecture:         s.OS.Uname.Machine,
		KernelVersion:              s.OS.Uname.Release,
		OSName:                     s.OS.ReleaseInfo["NAME"],
		OSVersion:                  s.OS.ReleaseInfo["VERSION_ID"],
		Project:                    projectName,
		Server:                     "lxd",
		ServerPid:                  os.Getpid(),
		ServerVersion:              version.Version,
		ServerLTS:                  version.IsLTSVersion,
		ServerClustered:            s.ServerClustered,
		ServerEventMode:            string(cluster.ServerEventMode()),
		ServerName:                 serverName,
		Firewall:                   s.Firewall.String(),
	}

	env.KernelFeatures = map[string]string{
		"bpf_token":                 strconv.FormatBool(s.OS.BPFToken),
		"netnsid_getifaddrs":        strconv.FormatBool(s.OS.NetnsGetifaddrs),
		"uevent_injection":          strconv.FormatBool(s.OS.UeventInjection),
		"unpriv_binfmt":             strconv.FormatBool(s.OS.UnprivBinfmt),
		"unpriv_fscaps":             strconv.FormatBool(s.OS.VFS3Fscaps),
		"seccomp_listener":          strconv.FormatBool(s.OS.SeccompListener),
		"seccomp_listener_continue": strconv.FormatBool(s.OS.SeccompListenerContinue),
		"idmapped_mounts":           strconv.FormatBool(s.OS.IdmappedMounts),
	}

	drivers := instanceDrivers.DriverStatuses()

	// Sort drivers map keys in order to produce consistent results.
	driverKeys := make([]instancetype.Type, 0, len(drivers))
	for k := range drivers {
		driverKeys = append(driverKeys, k)
	}

	slices.Sort(driverKeys)

	for _, key := range driverKeys {
		driver := drivers[key]

		// Only report the supported drivers.
		if !driver.Supported {
			continue
		}

		if env.Driver != "" {
			env.Driver = env.Driver + " | " + driver.Info.Name
		} else {
			env.Driver = driver.Info.Name
		}

		// Get the version of the instance drivers in use.
		if env.DriverVersion != "" {
			env.DriverVersion = env.DriverVersion + " | " + driver.Info.Version
		} else {
			env.DriverVersion = driver.Info.Version
		}

		// Add supported instance types.
		instType := driver.Info.Type.String()
		if !slices.Contains(env.InstanceTypes, instType) {
			env.InstanceTypes = append(env.InstanceTypes, instType)
		}
	}

	if s.OS.LXCFeatures != nil {
		env.LXCFeatures = map[string]string{}
		for k, v := range s.OS.LXCFeatures {
			env.LXCFeatures[k] = strconv.FormatBool(v)
		}
	}

	supportedStorageDrivers, usedStorageDrivers := readStoragePoolDriversCache()
	for driver, version := range usedStorageDrivers {
		if env.Storage != "" {
			env.Storage = env.Storage + " | " + driver
		} else {
			env.Storage = driver
		}

		// Get the version of the storage drivers in use.
		if env.StorageVersion != "" {
			env.StorageVersion = env.StorageVersion + " | " + version
		} else {
			env.StorageVersion = version
		}
	}

	env.StorageSupportedDrivers = supportedStorageDrivers

	fullSrv := &api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env
	fullSrv.AuthUserName = requestor.CallerUsername()
	fullSrv.AuthUserMethod = requestor.CallerProtocol()

	// Only allow identities that can edit configuration to view it as sensitive information may be stored there.
	err = s.Authorizer.CheckPermission(r.Context(), entity.ServerURL(), auth.EntitlementCanEdit)
	if err != nil && !auth.IsDeniedError(err) {
		return response.SmartError(err)
	} else if err == nil {
		fullSrv.Config, err = daemonConfigRender(s)
		if err != nil {
			return response.InternalError(err)
		}
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeServer, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ServerURL(): fullSrv})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, fullSrv, fullSrv.Config)
}

// swagger:operation PUT /1.0 server server_put
//
//	Update the server configuration
//
//	Updates the entire server configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: server
//	    description: Server configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ServerPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func api10Put(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	// Don't apply changes to settings until daemon is fully started.
	<-d.waitReady.Done()

	req := api.ServerPut{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// If this is a notification from a cluster node, just run the triggers
	// for reacting to the values that changed.
	if isClusterNotification(r) {
		logger.Debug("Handling config changed notification")
		changed := make(map[string]string)
		for key, value := range req.Config {
			changed[key], _ = value.(string)
		}

		// Get the current (updated) config.
		var config *clusterConfig.Config
		err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			config, err = clusterConfig.Load(ctx, tx)
			return err
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Update the daemon config.
		d.globalConfigMu.Lock()
		d.globalConfig = config
		d.globalConfigMu.Unlock()

		// Run any update triggers.
		err = doAPI10UpdateTriggers(d, nil, changed, s.LocalConfig, config)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}

	render, err := daemonConfigRender(s)
	if err != nil {
		return response.SmartError(err)
	}

	err = util.EtagCheck(r, render)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	return doAPI10Update(d, r, req, false)
}

// swagger:operation PATCH /1.0 server server_patch
//
//	Partially update the server configuration
//
//	Updates a subset of the server configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	  - in: body
//	    name: server
//	    description: Server configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ServerPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func api10Patch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	target := request.QueryParam(r, "target")
	resp := forwardedResponseToNode(r.Context(), s, target)
	if resp != nil {
		return resp
	}

	// Don't apply changes to settings until daemon is fully started.
	<-d.waitReady.Done()

	render, err := daemonConfigRender(s)
	if err != nil {
		return response.InternalError(err)
	}

	err = util.EtagCheck(r, render)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ServerPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Config == nil {
		return response.EmptySyncResponse
	}

	return doAPI10Update(d, r, req, true)
}

func doAPI10Update(d *Daemon, r *http.Request, req api.ServerPut, patch bool) response.Response {
	s := d.State()

	// First deal with config specific to the local daemon
	nodeValues := map[string]any{}

	for key := range node.ConfigSchema {
		value, ok := req.Config[key]
		if ok {
			nodeValues[key] = value
			delete(req.Config, key)
		}
	}

	nodeChanged := map[string]string{}
	var newNodeConfig *node.Config
	oldNodeConfig := make(map[string]any)

	err := s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		newNodeConfig, err = node.ConfigLoad(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to load node config: %w", err)
		}

		// Keep old config around in case something goes wrong. In that case the config will be reverted.
		maps.Copy(oldNodeConfig, newNodeConfig.Dump())

		// We currently don't allow changing the cluster.https_address once it's set.
		if s.ServerClustered {
			curConfig, err := tx.Config(ctx)
			if err != nil {
				return fmt.Errorf("Cannot fetch node config from database: %w", err)
			}

			newClusterHTTPSAddress := ""
			newClusterHTTPSAddressAny, found := nodeValues["cluster.https_address"]
			if found {
				newClusterHTTPSAddress, _ = newClusterHTTPSAddressAny.(string)
			} else if patch {
				newClusterHTTPSAddress = curConfig["cluster.https_address"]
			}

			if curConfig["cluster.https_address"] != newClusterHTTPSAddress {
				return errors.New("Changing cluster.https_address is currently not supported")
			}
		}

		// Validate the storage volumes
		if nodeValues["storage.backups_volume"] != nil && nodeValues["storage.backups_volume"] != newNodeConfig.StorageBackupsVolume() {
			backupsPoolVolume, ok := nodeValues["storage.backups_volume"].(string)
			if !ok {
				return fmt.Errorf(`Unexpected type for "storage.backups_volume": %T`, nodeValues["storage.backups_volume"])
			}

			// Store validated name back into nodeValues to ensure its not classifed as raw user input.
			nodeValues["storage.backups_volume"], err = daemonStorageValidate(s, backupsPoolVolume)
			if err != nil {
				return fmt.Errorf("Failed validation of %q: %w", "storage.backups_volume", err)
			}
		}

		if nodeValues["storage.images_volume"] != nil && nodeValues["storage.images_volume"] != newNodeConfig.StorageImagesVolume() {
			imagesPoolVolume, ok := nodeValues["storage.images_volume"].(string)
			if !ok {
				return fmt.Errorf(`Unexpected type for "storage.images_volume": %T`, nodeValues["storage.images_volume"])
			}

			// Store validated name back into nodeValues to ensure its not classifed as raw user input.
			nodeValues["storage.images_volume"], err = daemonStorageValidate(s, imagesPoolVolume)
			if err != nil {
				return fmt.Errorf("Failed validation of %q: %w", "storage.images_volume", err)
			}
		}

		if patch {
			nodeChanged, err = newNodeConfig.Patch(nodeValues)
		} else {
			nodeChanged, err = newNodeConfig.Replace(nodeValues)
		}

		return err
	})
	if err != nil {
		switch err.(type) {
		case config.ErrorList:
			return response.BadRequest(err)
		default:
			return response.SmartError(err)
		}
	}

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() {
		for key := range nodeValues {
			val, ok := oldNodeConfig[key]
			if !ok {
				nodeValues[key] = nil
			} else {
				nodeValues[key] = val
			}
		}

		err = s.DB.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
			newNodeConfig, err := node.ConfigLoad(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to load node config: %w", err)
			}

			_, err = newNodeConfig.Replace(nodeValues)
			if err != nil {
				return fmt.Errorf("Failed updating node config: %w", err)
			}

			return nil
		})

		if err != nil {
			logger.Warn("Failed reverting node config", logger.Ctx{"err": err})
		}
	})

	// Then deal with cluster wide configuration
	var clusterChanged map[string]string
	var newClusterConfig *clusterConfig.Config
	var oldClusterConfig map[string]any

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		newClusterConfig, err = clusterConfig.Load(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to load cluster config: %w", err)
		}

		// Keep old config around in case something goes wrong. In that case the config will be reverted.
		oldClusterConfig = newClusterConfig.Dump()

		if patch {
			clusterChanged, err = newClusterConfig.Patch(tx, req.Config)
		} else {
			clusterChanged, err = newClusterConfig.Replace(tx, req.Config)
		}

		return err
	})
	if err != nil {
		switch err.(type) {
		case config.ErrorList:
			return response.BadRequest(err)
		default:
			return response.SmartError(err)
		}
	}

	revert.Add(func() {
		for key := range req.Config {
			val, ok := oldClusterConfig[key]
			if !ok {
				req.Config[key] = nil
			} else {
				req.Config[key] = val
			}
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			newClusterConfig, err = clusterConfig.Load(ctx, tx)
			if err != nil {
				return fmt.Errorf("Failed to load cluster config: %w", err)
			}

			_, err = newClusterConfig.Replace(tx, req.Config)
			if err != nil {
				return fmt.Errorf("Failed updating cluster config: %w", err)
			}

			return nil
		})

		if err != nil {
			logger.Warn("Failed reverting cluster config", logger.Ctx{"err": err})
		}
	})

	// Notify the other nodes about changes
	notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAlive)
	if err != nil {
		return response.SmartError(err)
	}

	err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
		server, etag, err := client.GetServer()
		if err != nil {
			return err
		}

		serverPut := server.Writable()
		serverPut.Config = make(map[string]any)
		// Only propagated cluster-wide changes
		for key, value := range clusterChanged {
			serverPut.Config[key] = value
		}

		return client.UpdateServer(serverPut, etag)
	})
	if err != nil {
		logger.Error("Failed to notify other members about config change", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	// Update the daemon config.
	d.globalConfigMu.Lock()
	d.globalConfig = newClusterConfig
	d.localConfig = newNodeConfig
	d.globalConfigMu.Unlock()

	// Run any update triggers.
	err = doAPI10UpdateTriggers(d, nodeChanged, clusterChanged, newNodeConfig, newClusterConfig)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()

	s.Events.SendLifecycle("", lifecycle.ConfigUpdated.Event(request.CreateRequestor(r.Context()), nil))

	return response.EmptySyncResponse
}

func doAPI10UpdateTriggers(d *Daemon, nodeChanged, clusterChanged map[string]string, newNodeConfig *node.Config, newClusterConfig *clusterConfig.Config) error {
	s := d.State()

	maasChanged := false
	bgpChanged := false
	dnsChanged := false
	lokiChanged := false
	acmeDomainChanged := false
	acmeCAURLChanged := false
	oidcChanged := false
	syslogSocketChanged := false

	for key := range clusterChanged {
		switch key {
		case "core.https_trusted_proxy":
			s.Endpoints.NetworkUpdateTrustedProxy(clusterChanged[key])
		case "core.proxy_http":
			fallthrough
		case "core.proxy_https":
			fallthrough
		case "core.proxy_ignore_hosts":
			daemonConfigSetProxy(d, newClusterConfig)
		case "maas.api.url":
			fallthrough
		case "maas.api.key":
			maasChanged = true
		case "cluster.images_minimal_replica":
			err := autoSyncImages(s.ShutdownCtx, s)
			if err != nil {
				logger.Warn("Could not auto-sync images", logger.Ctx{"err": err})
			}

		case "cluster.offline_threshold":
			d.gateway.HeartbeatOfflineThreshold = newClusterConfig.OfflineThreshold()
			d.taskClusterHeartbeat.Reset()
		case "images.auto_update_interval":
			fallthrough
		case "images.remote_cache_expiry":
			if !s.OS.MockMode {
				d.taskPruneImages.Reset()
			}

		case "core.bgp_asn":
			bgpChanged = true
		case "loki.api.url":
			fallthrough
		case "loki.auth.username":
			fallthrough
		case "loki.auth.password":
			fallthrough
		case "loki.api.ca_cert":
			fallthrough
		case "loki.instance":
			fallthrough
		case "loki.labels":
			fallthrough
		case "loki.loglevel":
			fallthrough
		case "loki.types":
			lokiChanged = true
		case "acme.ca_url":
			acmeCAURLChanged = true
		case "acme.domain":
			acmeDomainChanged = true
		case "oidc.issuer", "oidc.client.id", "oidc.client.secret", "oidc.scopes", "oidc.audience", "oidc.groups.claim":
			oidcChanged = true
		}
	}

	for key := range nodeChanged {
		switch key {
		case "maas.machine":
			maasChanged = true
		case "core.bgp_address":
			fallthrough
		case "core.bgp_routerid":
			bgpChanged = true
		case "core.dns_address":
			dnsChanged = true
		case "core.syslog_socket":
			syslogSocketChanged = true
		}
	}

	// Process some additional keys. We do it sequentially because some keys are
	// correlated with others, and need to be processed first (for example
	// core.https_address need to be processed before
	// cluster.https_address).

	value, ok := nodeChanged["core.https_address"]
	if ok {
		err := s.Endpoints.NetworkUpdateAddress(value)
		if err != nil {
			return err
		}

		s.Endpoints.NetworkUpdateTrustedProxy(newClusterConfig.HTTPSTrustedProxy())
	}

	value, ok = nodeChanged["cluster.https_address"]
	if ok {
		err := s.Endpoints.ClusterUpdateAddress(value)
		if err != nil {
			return err
		}

		s.Endpoints.NetworkUpdateTrustedProxy(newClusterConfig.HTTPSTrustedProxy())
	}

	value, ok = nodeChanged["core.debug_address"]
	if ok {
		err := s.Endpoints.PprofUpdateAddress(value)
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["core.metrics_address"]
	if ok {
		err := s.Endpoints.MetricsUpdateAddress(value, s.Endpoints.NetworkCert())
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["core.storage_buckets_address"]
	if ok {
		err := s.Endpoints.StorageBucketsUpdateAddress(value, s.Endpoints.NetworkCert())
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["storage.backups_volume"]
	if ok {
		err := daemonStorageMove(s, "backups", value)
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["storage.images_volume"]
	if ok {
		err := daemonStorageMove(s, "images", value)
		if err != nil {
			return err
		}
	}

	if maasChanged {
		url, key := newClusterConfig.MAASController()
		machine := newNodeConfig.MAASMachine()
		err := d.setupMAASController(url, key, machine)
		if err != nil {
			return err
		}
	}

	if bgpChanged {
		address := newNodeConfig.BGPAddress()
		asn := newClusterConfig.BGPASN()
		routerid := newNodeConfig.BGPRouterID()

		if asn > math.MaxUint32 {
			return errors.New("Cannot convert BGP ASN to uint32: Upper bound exceeded")
		}

		err := s.BGP.Configure(address, uint32(asn), net.ParseIP(routerid))
		if err != nil {
			return fmt.Errorf("Failed reconfiguring BGP: %w", err)
		}
	}

	if dnsChanged {
		address := newNodeConfig.DNSAddress()

		err := s.DNS.Reconfigure(address)
		if err != nil {
			return fmt.Errorf("Failed reconfiguring DNS: %w", err)
		}
	}

	if lokiChanged {
		lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiInstance, lokiLoglevel, lokiLabels, lokiTypes := newClusterConfig.LokiServer()

		if lokiURL == "" || lokiLoglevel == "" || len(lokiTypes) == 0 {
			d.internalListener.RemoveHandler("loki")
		} else {
			err := d.setupLoki(lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiInstance, lokiLoglevel, lokiLabels, lokiTypes)
			if err != nil {
				return err
			}
		}
	}

	if acmeCAURLChanged || acmeDomainChanged {
		err := autoRenewCertificate(s.ShutdownCtx, d, acmeCAURLChanged)
		if err != nil {
			return err
		}
	}

	// Compile and load the instance placement scriptlet.
	value, ok = clusterChanged["instances.placement.scriptlet"]
	if ok {
		err := scriptletLoad.InstancePlacementSet(value)
		if err != nil {
			return fmt.Errorf("Failed saving instance placement scriptlet: %w", err)
		}
	}

	if oidcChanged {
		oidcIssuer, oidcClientID, oidcClientSecret, oidcScopes, oidcAudience, oidcGroupsClaim := newClusterConfig.OIDCServer()

		if oidcIssuer == "" || oidcClientID == "" {
			d.oidcVerifier = nil
		} else {
			var err error

			httpClientFunc := func() (*http.Client, error) {
				return util.HTTPClient("", d.proxy)
			}

			d.oidcVerifier, err = oidc.NewVerifier(oidcIssuer, oidcClientID, oidcClientSecret, oidcScopes, oidcAudience, s.ServerCert, d.identityCache, httpClientFunc, &oidc.Opts{GroupsClaim: oidcGroupsClaim})
			if err != nil {
				return fmt.Errorf("Failed creating verifier: %w", err)
			}
		}
	}

	if syslogSocketChanged {
		err := d.setupSyslogSocket(newNodeConfig.SyslogSocket())
		if err != nil {
			return err
		}
	}

	return nil
}
