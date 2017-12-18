package main

import (
	"fmt"
	"net/http"
	"os"
	"reflect"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/version"
)

var api10 = []Command{
	containersCmd,
	containerCmd,
	containerConsoleCmd,
	containerStateCmd,
	containerFileCmd,
	containerLogsCmd,
	containerLogCmd,
	containerSnapshotsCmd,
	containerSnapshotCmd,
	containerExecCmd,
	containerMetadataCmd,
	containerMetadataTemplatesCmd,
	aliasCmd,
	aliasesCmd,
	eventsCmd,
	imageCmd,
	imagesCmd,
	imagesExportCmd,
	imagesSecretCmd,
	imagesRefreshCmd,
	operationsCmd,
	operationCmd,
	operationWait,
	operationWebsocket,
	networksCmd,
	networkCmd,
	api10Cmd,
	certificatesCmd,
	certificateFingerprintCmd,
	profilesCmd,
	profileCmd,
	serverResourceCmd,
	storagePoolsCmd,
	storagePoolCmd,
	storagePoolResourcesCmd,
	storagePoolVolumesCmd,
	storagePoolVolumesTypeCmd,
	storagePoolVolumeTypeCmd,
	serverResourceCmd,
}

func api10Get(d *Daemon, r *http.Request) Response {
	authMethods := []string{"tls"}
	if daemonConfig["core.macaroon.endpoint"].Get() != "" {
		authMethods = append(authMethods, "macaroons")
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

	addresses, err := util.ListenAddresses(daemonConfig["core.https_address"].Get())
	if err != nil {
		return InternalError(err)
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
		Server:                 "lxd",
		ServerPid:              os.Getpid(),
		ServerVersion:          version.Version}

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
	fullSrv.Config = daemonConfigRender()

	return SyncResponseETag(true, fullSrv, fullSrv.Config)
}

func api10Put(d *Daemon, r *http.Request) Response {
	oldConfig, err := db.ConfigValuesGet(d.db.DB())
	if err != nil {
		return SmartError(err)
	}

	err = util.EtagCheck(r, daemonConfigRender())
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.ServerPut{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	return doApi10Update(d, oldConfig, req)
}

func api10Patch(d *Daemon, r *http.Request) Response {
	oldConfig, err := db.ConfigValuesGet(d.db.DB())
	if err != nil {
		return SmartError(err)
	}

	err = util.EtagCheck(r, daemonConfigRender())
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

	for k, v := range oldConfig {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	return doApi10Update(d, oldConfig, req)
}

func doApi10Update(d *Daemon, oldConfig map[string]string, req api.ServerPut) Response {
	// Deal with special keys
	for k, v := range req.Config {
		config := daemonConfig[k]
		if config != nil && config.hiddenValue && v == true {
			req.Config[k] = oldConfig[k]
		}
	}

	// Diff the configs
	changedConfig := map[string]interface{}{}
	for key, value := range oldConfig {
		if req.Config[key] != value {
			changedConfig[key] = req.Config[key]
		}
	}

	for key, value := range req.Config {
		if oldConfig[key] != value {
			changedConfig[key] = req.Config[key]
		}
	}

	for key, valueRaw := range changedConfig {
		if valueRaw == nil {
			valueRaw = ""
		}

		s := reflect.ValueOf(valueRaw)
		if !s.IsValid() || s.Kind() != reflect.String {
			return BadRequest(fmt.Errorf("Invalid value type for '%s'", key))
		}

		value := valueRaw.(string)

		confKey, ok := daemonConfig[key]
		if !ok {
			return BadRequest(fmt.Errorf("Bad server config key: '%s'", key))
		}

		err := confKey.Set(d, value)
		if err != nil {
			return SmartError(err)
		}
	}

	return EmptySyncResponse
}

var api10Cmd = Command{name: "", untrustedGet: true, get: api10Get, put: api10Put, patch: api10Patch}
