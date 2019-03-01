package main

import (
	"fmt"
	"net/http"
	"os"

	"gopkg.in/lxc/go-lxc.v2"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/config"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/version"
	"github.com/pkg/errors"
)

var api10Cmd = Command{
	name:         "",
	untrustedGet: true,
	get:          api10Get,
	put:          api10Put,
	patch:        api10Patch,
}

var api10 = []Command{
	aliasCmd,
	aliasesCmd,
	api10Cmd,
	certificateFingerprintCmd,
	certificatesCmd,
	clusterCmd,
	clusterNodeCmd,
	clusterNodesCmd,
	containerBackupCmd,
	containerBackupExportCmd,
	containerBackupsCmd,
	containerCmd,
	containerConsoleCmd,
	containerExecCmd,
	containerFileCmd,
	containerLogCmd,
	containerLogsCmd,
	containerMetadataCmd,
	containerMetadataTemplatesCmd,
	containersCmd,
	containerSnapshotCmd,
	containerSnapshotsCmd,
	containerStateCmd,
	eventsCmd,
	imageCmd,
	imageExportCmd,
	imageRefreshCmd,
	imagesCmd,
	imageSecretCmd,
	networkCmd,
	networkLeasesCmd,
	networksCmd,
	networkStateCmd,
	operationCmd,
	operationsCmd,
	operationWait,
	operationWebsocket,
	profileCmd,
	profilesCmd,
	projectCmd,
	projectsCmd,
	serverResourceCmd,
	serverResourceCmd,
	storagePoolCmd,
	storagePoolResourcesCmd,
	storagePoolsCmd,
	storagePoolVolumesCmd,
	storagePoolVolumesTypeCmd,
	storagePoolVolumeTypeContainerCmd,
	storagePoolVolumeTypeCustomCmd,
	storagePoolVolumeTypeImageCmd,
	storagePoolVolumeSnapshotsTypeCmd,
	storagePoolVolumeSnapshotTypeCmd,
}

func api10Get(d *Daemon, r *http.Request) Response {
	authMethods := []string{"tls"}
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		config, err := cluster.ConfigLoad(tx)
		if err != nil {
			return err
		}
		if config.CandidEndpoint() != "" {
			authMethods = append(authMethods, "candid")
		}
		return nil
	})
	if err != nil {
		return SmartError(err)
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
		return SyncResponseETag(true, srv, nil)
	}

	srv.Auth = "trusted"

	uname, err := shared.Uname()
	if err != nil {
		return InternalError(err)
	}

	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return InternalError(err)
	}
	addresses, err := util.ListenAddresses(address)
	if err != nil {
		return InternalError(err)
	}

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return SmartError(err)
	}

	// When clustered, use the node name, otherwise use the hostname.
	var serverName string
	if clustered {
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			serverName, err = tx.NodeName()
			return err
		})
		if err != nil {
			return SmartError(err)
		}
	} else {
		hostname, err := os.Hostname()
		if err != nil {
			return SmartError(err)
		}
		serverName = hostname
	}

	certificate := string(d.endpoints.NetworkPublicKey())
	var certificateFingerprint string
	if certificate != "" {
		certificateFingerprint, err = shared.CertFingerprintStr(certificate)
		if err != nil {
			return InternalError(err)
		}
	}

	architectures := []string{}

	for _, architecture := range d.os.Architectures {
		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			return InternalError(err)
		}
		architectures = append(architectures, architectureName)
	}

	project := r.FormValue("project")
	if project == "" {
		project = "default"
	}

	env := api.ServerEnvironment{
		Addresses:              addresses,
		Architectures:          architectures,
		Certificate:            certificate,
		CertificateFingerprint: certificateFingerprint,
		Driver:                 "lxc",
		DriverVersion:          lxc.Version(),
		Kernel:                 uname.Sysname,
		KernelArchitecture:     uname.Machine,
		KernelVersion:          uname.Release,
		Project:                project,
		Server:                 "lxd",
		ServerPid:              os.Getpid(),
		ServerVersion:          version.Version,
		ServerClustered:        clustered,
		ServerName:             serverName,
	}

	drivers := readStoragePoolDriversCache()
	for driver, version := range drivers {
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

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env
	fullSrv.Config, err = daemonConfigRender(d.State())
	if err != nil {
		return InternalError(err)
	}

	return SyncResponseETag(true, fullSrv, fullSrv.Config)
}

func api10Put(d *Daemon, r *http.Request) Response {
	req := api.ServerPut{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	// If this is a notification from a cluster node, just run the triggers
	// for reacting to the values that changed.
	if isClusterNotification(r) {
		logger.Debugf("Handling config changed notification")
		changed := make(map[string]string)
		for key, value := range req.Config {
			changed[key] = value.(string)
		}
		var config *cluster.Config
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			config, err = cluster.ConfigLoad(tx)
			return err
		})
		if err != nil {
			return SmartError(err)
		}
		err = doApi10UpdateTriggers(d, nil, changed, nil, config)
		if err != nil {
			return SmartError(err)
		}
		return EmptySyncResponse
	}

	render, err := daemonConfigRender(d.State())
	if err != nil {
		return SmartError(err)
	}
	err = util.EtagCheck(r, render)
	if err != nil {
		return PreconditionFailed(err)
	}

	return doApi10Update(d, req, false)
}

func api10Patch(d *Daemon, r *http.Request) Response {
	render, err := daemonConfigRender(d.State())
	if err != nil {
		return InternalError(err)
	}
	err = util.EtagCheck(r, render)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.ServerPut{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	if req.Config == nil {
		return EmptySyncResponse
	}

	return doApi10Update(d, req, true)
}

func doApi10Update(d *Daemon, req api.ServerPut, patch bool) Response {
	// First deal with config specific to the local daemon
	nodeValues := map[string]interface{}{}

	for key := range node.ConfigSchema {
		value, ok := req.Config[key]
		if ok {
			nodeValues[key] = value
			delete(req.Config, key)
		}
	}

	nodeChanged := map[string]string{}
	var newNodeConfig *node.Config
	err := d.db.Transaction(func(tx *db.NodeTx) error {
		var err error
		newNodeConfig, err = node.ConfigLoad(tx)
		if err != nil {
			return errors.Wrap(err, "Failed to load node config")
		}

		// We currently don't allow changing the cluster.https_address
		// once it's set.
		curClusterAddress := newNodeConfig.ClusterAddress()
		newClusterAddress, ok := nodeValues["cluster.https_address"]

		if ok && curClusterAddress != "" && !util.IsAddressCovered(newClusterAddress.(string), curClusterAddress) {
			return fmt.Errorf("Changing cluster.https_address is currently not supported")
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
			return BadRequest(err)
		default:
			return SmartError(err)
		}
	}

	// Then deal with cluster wide configuration
	var clusterChanged map[string]string
	var newClusterConfig *cluster.Config
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		newClusterConfig, err = cluster.ConfigLoad(tx)
		if err != nil {
			return errors.Wrap(err, "Failed to load cluster config")
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
			return BadRequest(err)
		default:
			return SmartError(err)
		}
	}

	// Notify the other nodes about changes
	notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), cluster.NotifyAlive)
	if err != nil {
		return SmartError(err)
	}
	err = notifier(func(client lxd.ContainerServer) error {
		server, etag, err := client.GetServer()
		if err != nil {
			return err
		}
		serverPut := server.Writable()
		serverPut.Config = make(map[string]interface{})
		// Only propagated cluster-wide changes
		for key, value := range clusterChanged {
			serverPut.Config[key] = value
		}
		return client.UpdateServer(serverPut, etag)
	})
	if err != nil {
		logger.Debugf("Failed to notify other nodes about config change: %v", err)
		return SmartError(err)
	}

	err = doApi10UpdateTriggers(d, nodeChanged, clusterChanged, newNodeConfig, newClusterConfig)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func doApi10UpdateTriggers(d *Daemon, nodeChanged, clusterChanged map[string]string, nodeConfig *node.Config, clusterConfig *cluster.Config) error {
	maasChanged := false
	candidChanged := false

	for key := range clusterChanged {
		switch key {
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
		case "images.auto_update_interval":
			if !d.os.MockMode {
				d.taskAutoUpdate.Reset()
			}
		case "images.remote_cache_expiry":
			if !d.os.MockMode {
				d.taskPruneImages.Reset()
			}
		}
	}

	// Look for changed values. We do it sequentially because some keys are
	// correlated with others, and need to be processed first (for example
	// core.https_address need to be processed before
	// cluster.https_address).

	_, ok := nodeChanged["maas.machine"]
	if ok {
		maasChanged = true
	}

	value, ok := nodeChanged["core.https_address"]
	if ok {
		err := d.endpoints.NetworkUpdateAddress(value)
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["cluster.https_address"]
	if ok {
		err := d.endpoints.ClusterUpdateAddress(value)
		if err != nil {
			return err
		}
	}

	value, ok = nodeChanged["core.debug_address"]
	if ok {
		err := d.endpoints.PprofUpdateAddress(value)
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
		endpoint := clusterConfig.CandidEndpoint()
		endpointKey := clusterConfig.CandidEndpointKey()
		expiry := clusterConfig.CandidExpiry()
		domains := clusterConfig.CandidDomains()

		err := d.setupExternalAuthentication(endpoint, endpointKey, expiry, domains)
		if err != nil {
			return err
		}
	}

	return nil
}
