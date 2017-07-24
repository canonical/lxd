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
	storagePoolsCmd,
	storagePoolCmd,
	storagePoolVolumesCmd,
	storagePoolVolumesTypeCmd,
	storagePoolVolumeTypeCmd,
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
			"container_exec_recording",
			"certificate_update",
			"container_exec_signal_handling",
			"gpu_devices",
			"container_image_properties",
			"migration_progress",
			"id_map",
			"network_firewall_filtering",
			"network_routes",
			"storage",
			"file_delete",
			"file_append",
			"network_dhcp_expiry",
			"storage_lvm_vg_rename",
			"storage_lvm_thinpool_rename",
			"network_vlan",
			"image_create_aliases",
			"container_stateless_copy",
			"container_only_migration",
			"storage_zfs_clone_copy",
			"unix_device_rename",
			"storage_lvm_use_thinpool",
			"storage_rsync_bwlimit",
			"network_vxlan_interface",
			"storage_btrfs_mount_options",
			"entity_description",
			"image_force_refresh",
			"storage_lvm_lv_resizing",
			"id_map_base",
			"file_symlinks",
			"container_push_target",
			"network_vlan_physical",
			"storage_images_delete",
			"container_edit_metadata",
			"container_snapshot_stateful_migration",
			"storage_driver_ceph",
		},
		APIStatus:  "stable",
		APIVersion: version.APIVersion,
		Public:     false,
		Auth:       "untrusted",
	}

	// If untrusted, return now
	if !d.isTrustedClient(r) {
		return SyncResponseETag(true, srv, nil)
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
		Server:                 "lxd",
		ServerPid:              os.Getpid(),
		ServerVersion:          version.Version}

	drivers := readStoragePoolDriversCache()
	for _, driver := range drivers {
		// Initialize a core storage interface for the given driver.
		sCore, err := storageCoreInit(driver)
		if err != nil {
			continue
		}

		if env.Storage != "" {
			env.Storage = env.Storage + " | " + driver
		} else {
			env.Storage = driver
		}

		// Get the version of the storage drivers in use.
		sVersion := sCore.GetStorageTypeVersion()
		if env.StorageVersion != "" {
			env.StorageVersion = env.StorageVersion + " | " + sVersion
		} else {
			env.StorageVersion = sVersion
		}
	}

	fullSrv := api.Server{ServerUntrusted: srv}
	fullSrv.Environment = env
	fullSrv.Config = daemonConfigRender()

	return SyncResponseETag(true, fullSrv, fullSrv.Config)
}

func api10Put(d *Daemon, r *http.Request) Response {
	oldConfig, err := dbConfigValuesGet(d.db)
	if err != nil {
		return SmartError(err)
	}

	err = etagCheck(r, daemonConfigRender())
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
	oldConfig, err := dbConfigValuesGet(d.db)
	if err != nil {
		return SmartError(err)
	}

	err = etagCheck(r, daemonConfigRender())
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
