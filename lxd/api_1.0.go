package main

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"syscall"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/version"
)

var api10 = []Command{
	containersCmd,
	containerCmd,
	containerStateCmd,
	containerFileCmd,
	containerLogsCmd,
	containerLogCmd,
	containerSnapshotsCmd,
	containerSnapshotCmd,
	containerExecCmd,
	aliasCmd,
	aliasesCmd,
	eventsCmd,
	imageCmd,
	imagesCmd,
	imagesExportCmd,
	imagesSecretCmd,
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
}

func api10Get(d *Daemon, r *http.Request) Response {
	srv := api.ServerUntrusted{
		/* List of API extensions in the order they were added.
		 *
		 * The following kind of changes require an addition to api_extensions:
		 *  - New configuration key
		 *  - New valid values for a configuration key
		 *  - New REST API endpoint
		 *  - New argument inside an existing REST API call
		 *  - New HTTPs authentication mechanisms or protocols
		 */
		APIExtensions: []string{
			"id_map",
		},
		APIStatus:  "stable",
		APIVersion: version.APIVersion,
		Public:     false,
		Auth:       "untrusted",
	}

	// If untrusted, return now
	if !d.isTrustedClient(r) {
		return SyncResponse(true, srv)
	}

	srv.Auth = "trusted"

	/*
	 * Based on: https://groups.google.com/forum/#!topic/golang-nuts/Jel8Bb-YwX8
	 * there is really no better way to do this, which is
	 * unfortunate. Also, we ditch the more accepted CharsToString
	 * version in that thread, since it doesn't seem as portable,
	 * viz. github issue #206.
	 */
	uname := syscall.Utsname{}
	if err := syscall.Uname(&uname); err != nil {
		return InternalError(err)
	}

	kernel := ""
	for _, c := range uname.Sysname {
		if c == 0 {
			break
		}
		kernel += string(byte(c))
	}

	kernelVersion := ""
	for _, c := range uname.Release {
		if c == 0 {
			break
		}
		kernelVersion += string(byte(c))
	}

	kernelArchitecture := ""
	for _, c := range uname.Machine {
		if c == 0 {
			break
		}
		kernelArchitecture += string(byte(c))
	}

	addresses, err := d.ListenAddresses()
	if err != nil {
		return InternalError(err)
	}

	var certificate string
	var certificateFingerprint string
	if len(d.tlsConfig.Certificates) != 0 {
		certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d.tlsConfig.Certificates[0].Certificate[0]}))
		certificateFingerprint, err = shared.CertFingerprintStr(certificate)
		if err != nil {
			return InternalError(err)
		}
	}

	architectures := []string{}

	for _, architecture := range d.architectures {
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
		Kernel:                 kernel,
		KernelArchitecture:     kernelArchitecture,
		KernelVersion:          kernelVersion,
		Storage:                d.Storage.GetStorageTypeName(),
		StorageVersion:         d.Storage.GetStorageTypeVersion(),
		Server:                 "lxd",
		ServerPid:              os.Getpid(),
		ServerVersion:          version.Version}

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env
	fullSrv.Config = daemonConfigRender()

	return SyncResponse(true, fullSrv)
}

func api10Put(d *Daemon, r *http.Request) Response {
	oldConfig, err := dbConfigValuesGet(d.db)
	if err != nil {
		return InternalError(err)
	}

	req := api.ServerPut{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

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
			return BadRequest(err)
		}
	}

	return EmptySyncResponse
}

var api10Cmd = Command{name: "", untrustedGet: true, get: api10Get, put: api10Put}
