package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	clusterConfig "github.com/lxc/lxd/lxd/cluster/config"
	"github.com/lxc/lxd/lxd/config"
	"github.com/lxc/lxd/lxd/db"
	instanceDrivers "github.com/lxc/lxd/lxd/instance/drivers"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	scriptletLoad "github.com/lxc/lxd/lxd/scriptlet/load"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/version"
)

var api10Cmd = APIEndpoint{
	Get:   APIEndpointAction{Handler: api10Get, AllowUntrusted: true},
	Patch: APIEndpointAction{Handler: api10Patch},
	Put:   APIEndpointAction{Handler: api10Put},
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
	instanceLogCmd,
	instanceLogsCmd,
	instanceMetadataCmd,
	instanceMetadataTemplatesCmd,
	instancesCmd,
	instanceSFTPCmd,
	instanceSnapshotCmd,
	instanceSnapshotsCmd,
	instanceStateCmd,
	eventsCmd,
	imageAliasCmd,
	imageAliasesCmd,
	imageCmd,
	imageExportCmd,
	imageRefreshCmd,
	imagesCmd,
	imageSecretCmd,
	networkCmd,
	networkLeasesCmd,
	networksCmd,
	networkStateCmd,
	networkACLCmd,
	networkACLsCmd,
	networkACLLogCmd,
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
	authMethods := []string{"tls"}
	candidURL, _, _, _ := s.GlobalConfig.CandidServer()
	rbacURL, _, _, _, _, _, _ := s.GlobalConfig.RBACServer()
	if candidURL != "" || rbacURL != "" {
		authMethods = append(authMethods, "candid")
	}

	srv := api.ServerUntrusted{
		APIExtensions: version.APIExtensions,
		APIStatus:     "stable",
		APIVersion:    version.APIVersion,
		Public:        false,
		Auth:          "untrusted",
		AuthMethods:   authMethods,
	}

	// If untrusted, return now
	if d.checkTrustedClient(r) != nil {
		return response.SyncResponseETag(true, srv, nil)
	}

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	srv.Auth = "trusted"

	localHTTPSAddress := s.LocalConfig.HTTPSAddress()

	addresses, err := util.ListenAddresses(localHTTPSAddress)
	if err != nil {
		return response.InternalError(err)
	}

	clustered, err := cluster.Enabled(d.db.Node)
	if err != nil {
		return response.SmartError(err)
	}

	// When clustered, use the node name, otherwise use the hostname.
	var serverName string
	if clustered {
		serverName = s.ServerName
	} else {
		hostname, err := os.Hostname()
		if err != nil {
			return response.SmartError(err)
		}

		serverName = hostname
	}

	certificate := string(d.endpoints.NetworkPublicKey())
	var certificateFingerprint string
	if certificate != "" {
		certificateFingerprint, err = shared.CertFingerprintStr(certificate)
		if err != nil {
			return response.InternalError(err)
		}
	}

	architectures := []string{}

	for _, architecture := range d.os.Architectures {
		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			return response.InternalError(err)
		}

		architectures = append(architectures, architectureName)
	}

	projectName := r.FormValue("project")
	if projectName == "" {
		projectName = project.Default
	}

	env := api.ServerEnvironment{
		Addresses:              addresses,
		Architectures:          architectures,
		Certificate:            certificate,
		CertificateFingerprint: certificateFingerprint,
		Kernel:                 d.os.Uname.Sysname,
		KernelArchitecture:     d.os.Uname.Machine,
		KernelVersion:          d.os.Uname.Release,
		OSName:                 d.os.ReleaseInfo["NAME"],
		OSVersion:              d.os.ReleaseInfo["VERSION_ID"],
		Project:                projectName,
		Server:                 "lxd",
		ServerPid:              os.Getpid(),
		ServerVersion:          version.Version,
		ServerClustered:        clustered,
		ServerEventMode:        string(cluster.ServerEventMode()),
		ServerName:             serverName,
		Firewall:               d.firewall.String(),
	}

	env.KernelFeatures = map[string]string{
		"netnsid_getifaddrs":        fmt.Sprintf("%v", d.os.NetnsGetifaddrs),
		"uevent_injection":          fmt.Sprintf("%v", d.os.UeventInjection),
		"unpriv_fscaps":             fmt.Sprintf("%v", d.os.VFS3Fscaps),
		"seccomp_listener":          fmt.Sprintf("%v", d.os.SeccompListener),
		"seccomp_listener_continue": fmt.Sprintf("%v", d.os.SeccompListenerContinue),
		"shiftfs":                   fmt.Sprintf("%v", d.os.Shiftfs),
		"idmapped_mounts":           fmt.Sprintf("%v", d.os.IdmappedMounts),
	}

	drivers := instanceDrivers.DriverStatuses()
	for _, driver := range drivers {
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
	}

	if d.os.LXCFeatures != nil {
		env.LXCFeatures = map[string]string{}
		for k, v := range d.os.LXCFeatures {
			env.LXCFeatures[k] = fmt.Sprintf("%v", v)
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

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env

	if rbac.UserIsAdmin(r) {
		fullSrv.Config, err = daemonConfigRender(s)
		if err != nil {
			return response.InternalError(err)
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
	resp := forwardedResponseIfTargetIsRemote(s, r)
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
			changed[key] = value.(string)
		}

		// Get the current (updated) config.
		var config *clusterConfig.Config
		err := d.db.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
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
		err = doApi10UpdateTriggers(d, nil, changed, nil, config)
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

	return doApi10Update(d, r, req, false)
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
	resp := forwardedResponseIfTargetIsRemote(s, r)
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

	return doApi10Update(d, r, req, true)
}

func doApi10Update(d *Daemon, r *http.Request, req api.ServerPut, patch bool) response.Response {
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

	clustered, err := cluster.Enabled(d.db.Node)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to check for cluster state: %w", err))
	}

	nodeChanged := map[string]string{}
	var newNodeConfig *node.Config
	err = d.db.Node.Transaction(r.Context(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		newNodeConfig, err = node.ConfigLoad(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to load node config: %w", err)
		}

		// We currently don't allow changing the cluster.https_address once it's set.
		if clustered {
			curConfig, err := tx.Config(ctx)
			if err != nil {
				return fmt.Errorf("Cannot fetch node config from database: %w", err)
			}

			newClusterHTTPSAddress, found := nodeValues["cluster.https_address"]
			if !found && patch {
				newClusterHTTPSAddress = curConfig["cluster.https_address"]
			} else if !found {
				newClusterHTTPSAddress = ""
			}

			if curConfig["cluster.https_address"] != newClusterHTTPSAddress.(string) {
				return fmt.Errorf("Changing cluster.https_address is currently not supported")
			}
		}

		// Validate the storage volumes
		if nodeValues["storage.backups_volume"] != nil && nodeValues["storage.backups_volume"] != newNodeConfig.StorageBackupsVolume() {
			err := daemonStorageValidate(s, nodeValues["storage.backups_volume"].(string))
			if err != nil {
				return fmt.Errorf("Failed validation of %q: %w", "storage.backups_volume", err)
			}
		}

		if nodeValues["storage.images_volume"] != nil && nodeValues["storage.images_volume"] != newNodeConfig.StorageImagesVolume() {
			err := daemonStorageValidate(s, nodeValues["storage.images_volume"].(string))
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

	// Validate global configuration
	hasRBAC := false
	hasCandid := false
	for k, v := range req.Config {
		if v == "" {
			continue
		}

		if strings.HasPrefix(k, "candid.") {
			hasCandid = true
		} else if strings.HasPrefix(k, "rbac.") {
			hasRBAC = true
		}

		if hasCandid && hasRBAC {
			return response.BadRequest(fmt.Errorf("RBAC and Candid are mutually exclusive"))
		}
	}

	// Then deal with cluster wide configuration
	var clusterChanged map[string]string
	var newClusterConfig *clusterConfig.Config
	err = d.db.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		newClusterConfig, err = clusterConfig.Load(ctx, tx)
		if err != nil {
			return fmt.Errorf("Failed to load cluster config: %w", err)
		}

		if patch {
			clusterChanged, err = newClusterConfig.Patch(req.Config)
		} else {
			clusterChanged, err = newClusterConfig.Replace(req.Config)
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

	// Notify the other nodes about changes
	notifier, err := cluster.NewNotifier(s, d.endpoints.NetworkCert(), d.serverCert(), cluster.NotifyAlive)
	if err != nil {
		return response.SmartError(err)
	}

	err = notifier(func(client lxd.InstanceServer) error {
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
	err = doApi10UpdateTriggers(d, nodeChanged, clusterChanged, newNodeConfig, newClusterConfig)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(project.Default, lifecycle.ConfigUpdated.Event(request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

func doApi10UpdateTriggers(d *Daemon, nodeChanged, clusterChanged map[string]string, nodeConfig *node.Config, clusterConfig *clusterConfig.Config) error {
	s := d.State()

	maasChanged := false
	candidChanged := false
	rbacChanged := false
	bgpChanged := false
	dnsChanged := false
	lokiChanged := false
	acmeDomainChanged := false
	acmeCAURLChanged := false

	for key := range clusterChanged {
		switch key {
		case "core.https_trusted_proxy":
			d.endpoints.NetworkUpdateTrustedProxy(clusterChanged[key])
		case "core.proxy_http":
			fallthrough
		case "core.proxy_https":
			fallthrough
		case "core.proxy_ignore_hosts":
			daemonConfigSetProxy(d, clusterConfig)
		case "maas.api.url":
			fallthrough
		case "maas.api.key":
			maasChanged = true
		case "candid.domains":
			fallthrough
		case "candid.expiry":
			fallthrough
		case "candid.api.key":
			fallthrough
		case "candid.api.url":
			candidChanged = true
		case "cluster.images_minimal_replica":
			err := autoSyncImages(d.shutdownCtx, d)
			if err != nil {
				logger.Warn("Could not auto-sync images", logger.Ctx{"err": err})
			}

		case "cluster.offline_threshold":
			d.gateway.HeartbeatOfflineThreshold = clusterConfig.OfflineThreshold()
			d.taskClusterHeartbeat.Reset()
		case "images.auto_update_interval":
			fallthrough
		case "images.remote_cache_expiry":
			if !d.os.MockMode {
				d.taskPruneImages.Reset()
			}

		case "rbac.agent.url":
			fallthrough
		case "rbac.agent.username":
			fallthrough
		case "rbac.agent.private_key":
			fallthrough
		case "rbac.agent.public_key":
			fallthrough
		case "rbac.api.url":
			fallthrough
		case "rbac.api.key":
			fallthrough
		case "rbac.expiry":
			rbacChanged = true
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
		}
	}

	// Process some additional keys. We do it sequentially because some keys are
	// correlated with others, and need to be processed first (for example
	// core.https_address need to be processed before
	// cluster.https_address).

	value, ok := nodeChanged["core.https_address"]
	if ok {
		err := d.endpoints.NetworkUpdateAddress(value)
		if err != nil {
			return err
		}

		d.endpoints.NetworkUpdateTrustedProxy(clusterConfig.HTTPSTrustedProxy())
	}

	value, ok = nodeChanged["cluster.https_address"]
	if ok {
		err := d.endpoints.ClusterUpdateAddress(value)
		if err != nil {
			return err
		}

		d.endpoints.NetworkUpdateTrustedProxy(clusterConfig.HTTPSTrustedProxy())
	}

	value, ok = nodeChanged["core.debug_address"]
	if ok {
		err := d.endpoints.PprofUpdateAddress(value)
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["core.metrics_address"]
	if ok {
		err := d.endpoints.MetricsUpdateAddress(value, d.endpoints.NetworkCert())
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["core.storage_buckets_address"]
	if ok {
		err := d.endpoints.StorageBucketsUpdateAddress(value, d.endpoints.NetworkCert())
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
		url, key := clusterConfig.MAASController()
		machine := nodeConfig.MAASMachine()
		err := d.setupMAASController(url, key, machine)
		if err != nil {
			return err
		}
	}

	if candidChanged {
		apiURL, apiKey, expiry, domains := clusterConfig.CandidServer()
		err := d.setupExternalAuthentication(apiURL, apiKey, expiry, domains)
		if err != nil {
			return err
		}
	}

	if rbacChanged {
		apiURL, apiKey, apiExpiry, agentURL, agentUsername, agentPrivateKey, agentPublicKey := clusterConfig.RBACServer()

		// Since RBAC seems to have been set up already, we need to disable it temporarily
		if d.rbac != nil {
			err := d.setupExternalAuthentication("", "", 0, "")
			if err != nil {
				return err
			}

			d.rbac.StopStatusCheck()
			d.rbac = nil
		}

		err := d.setupRBACServer(apiURL, apiKey, apiExpiry, agentURL, agentUsername, agentPrivateKey, agentPublicKey)
		if err != nil {
			return err
		}
	}

	if bgpChanged {
		address := nodeConfig.BGPAddress()
		asn := clusterConfig.BGPASN()
		routerid := nodeConfig.BGPRouterID()

		err := s.BGP.Reconfigure(address, uint32(asn), net.ParseIP(routerid))
		if err != nil {
			return fmt.Errorf("Failed reconfiguring BGP: %w", err)
		}
	}

	if dnsChanged {
		address := nodeConfig.DNSAddress()

		err := s.DNS.Reconfigure(address)
		if err != nil {
			return fmt.Errorf("Failed reconfiguring DNS: %w", err)
		}
	}

	if lokiChanged {
		lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiLabels, lokiLoglevel, lokiTypes := clusterConfig.LokiServer()

		if lokiURL == "" || lokiLoglevel == "" || len(lokiTypes) == 0 {
			d.internalListener.RemoveHandler("loki")
		} else {
			err := d.setupLoki(lokiURL, lokiUsername, lokiPassword, lokiCACert, lokiLabels, lokiLoglevel, lokiTypes)
			if err != nil {
				return err
			}
		}
	}

	if acmeCAURLChanged || acmeDomainChanged {
		err := autoRenewCertificate(d.shutdownCtx, d, acmeCAURLChanged)
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

	return nil
}
