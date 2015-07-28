package main

import (
	"fmt"
	"net/http"
	"syscall"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

var api10 = []Command{
	containersCmd,
	containerCmd,
	containerStateCmd,
	containerFileCmd,
	containerSnapshotsCmd,
	containerSnapshotCmd,
	containerExecCmd,
	aliasCmd,
	aliasesCmd,
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
	body := shared.Jmap{"api_compat": shared.APICompat}

	if d.isTrustedClient(r) {
		body["auth"] = "trusted"

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

		env := shared.Jmap{
			"architectures":       d.architectures,
			"backing_fs":          d.BackingFs,
			"driver":              "lxc",
			"driver_version":      lxc.Version(),
			"kernel":              kernel,
			"kernel_architecture": kernelArchitecture,
			"kernel_version":      kernelVersion,
			"version":             shared.Version}

		body["environment"] = env

		serverConfig, err := d.ConfigValuesGet()
		if err != nil {
			return InternalError(err)
		}

		config := shared.Jmap{}

		for key, value := range serverConfig {
			if key == "core.trust_password" {
				config[key] = true
			} else {
				config[key] = value
			}
		}

		body["config"] = config
	} else {
		body["auth"] = "untrusted"
	}

	return SyncResponse(true, body)
}

type apiPut struct {
	Config shared.Jmap `json:"config"`
}

func api10Put(d *Daemon, r *http.Request) Response {
	req := apiPut{}

	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	for key, value := range req.Config {
		if !d.ConfigKeyIsValid(key) {
			return BadRequest(fmt.Errorf("Bad server config key: '%s'", key))
		}

		if key == "core.trust_password" {
			err := d.PasswordSet(value.(string))
			if err != nil {
				return InternalError(err)
			}
		} else if key == "core.lvm_vg_name" {
			err := storageLVMSetVolumeGroupNameConfig(d, value.(string))
			if err != nil {
				return InternalError(err)
			}
			if err = d.SetupStorageDriver(); err != nil {
				return InternalError(err)
			}
		} else if key == "core.lvm_thinpool_name" {
			err := storageLVMSetThinPoolNameConfig(d, value.(string))
			if err != nil {
				return InternalError(err)
			}
		} else {
			err := d.ConfigValueSet(key, value.(string))
			if err != nil {
				return InternalError(err)
			}
		}
	}

	return EmptySyncResponse
}

var api10Cmd = Command{name: "", untrustedGet: true, get: api10Get, put: api10Put}
