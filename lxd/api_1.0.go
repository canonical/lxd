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
	body := shared.Jmap{
		/* List of API extensions in the order they were added.
		 *
		 * The following kind of changes require an addition to api_extensions:
		 *  - New configuration key
		 *  - New valid values for a configuration key
		 *  - New REST API endpoint
		 *  - New argument inside an existing REST API call
		 *  - New HTTPs authentication mechanisms or protocols
		 */
		"api_extensions": []string{
			"storage_zfs_remove_snapshots",
			"container_host_shutdown_timeout",
			"container_syscall_filtering",
			"auth_pki",
			"container_last_used_at",
			"etag",
			"patch",
			"usb_devices",
			"https_allowed_credentials",
			"image_compression_algorithm",
			"directory_manipulation",
			"container_cpu_time",
			"storage_zfs_use_refquota",
			"storage_lvm_mount_options",
			"network",
			"profile_usedby",
			"container_push",
		},

		"api_status":  "stable",
		"api_version": shared.APIVersion,
	}

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

		addresses, err := d.ListenAddresses()
		if err != nil {
			return InternalError(err)
		}

		var certificate string
		if len(d.tlsConfig.Certificates) != 0 {
			certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d.tlsConfig.Certificates[0].Certificate[0]}))
		}

		architectures := []string{}

		for _, architecture := range d.architectures {
			architectureName, err := shared.ArchitectureName(architecture)
			if err != nil {
				return InternalError(err)
			}
			architectures = append(architectures, architectureName)
		}

		env := shared.Jmap{
			"addresses":           addresses,
			"architectures":       architectures,
			"certificate":         certificate,
			"driver":              "lxc",
			"driver_version":      lxc.Version(),
			"kernel":              kernel,
			"kernel_architecture": kernelArchitecture,
			"kernel_version":      kernelVersion,
			"storage":             d.Storage.GetStorageTypeName(),
			"storage_version":     d.Storage.GetStorageTypeVersion(),
			"server":              "lxd",
			"server_pid":          os.Getpid(),
			"server_version":      shared.Version}

		body["environment"] = env
		body["public"] = false
		body["config"] = daemonConfigRender()
	} else {
		body["auth"] = "untrusted"
		body["public"] = false
	}

	return SyncResponseETag(true, body, body["config"])
}

type apiPut struct {
	Config shared.Jmap `json:"config"`
}

func api10Put(d *Daemon, r *http.Request) Response {
	oldConfig, err := dbConfigValuesGet(d.db)
	if err != nil {
		return InternalError(err)
	}

	err = etagCheck(r, oldConfig)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := apiPut{}
	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	return doApi10Update(d, oldConfig, req)
}

func api10Patch(d *Daemon, r *http.Request) Response {
	oldConfig, err := dbConfigValuesGet(d.db)
	if err != nil {
		return InternalError(err)
	}

	err = etagCheck(r, oldConfig)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := apiPut{}
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

func doApi10Update(d *Daemon, oldConfig map[string]string, req apiPut) Response {
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
