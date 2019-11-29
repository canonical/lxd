package version

import (
	"os"
	"strconv"
)

// APIVersion contains the API base version. Only bumped for backward incompatible changes.
var APIVersion = "1.0"

// APIExtensions is the list of all API extensions in the order they were added.
//
// The following kind of changes come with a new extensions:
//
// - New configuration key
// - New valid values for a configuration key
// - New REST API endpoint
// - New argument inside an existing REST API call
// - New HTTPs authentication mechanisms or protocols
//
// This list is used mainly by the LXD server code, but it's in the shared
// package as well for reference.
var APIExtensions = []string{
	"storage_zfs_remove_snapshots",
	"container_host_shutdown_timeout",
	"container_stop_priority",
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
	"storage_ceph_user_name",
	"resource_limits",
	"storage_volatile_initial_source",
	"storage_ceph_force_osd_reuse",
	"storage_block_filesystem_btrfs",
	"resources",
	"kernel_limits",
	"storage_api_volume_rename",
	"macaroon_authentication",
	"network_sriov",
	"console",
	"restrict_devlxd",
	"migration_pre_copy",
	"infiniband",
	"maas_network",
	"devlxd_events",
	"proxy",
	"network_dhcp_gateway",
	"file_get_symlink",
	"network_leases",
	"unix_device_hotplug",
	"storage_api_local_volume_handling",
	"operation_description",
	"clustering",
	"event_lifecycle",
	"storage_api_remote_volume_handling",
	"nvidia_runtime",
	"container_mount_propagation",
	"container_backup",
	"devlxd_images",
	"container_local_cross_pool_handling",
	"proxy_unix",
	"proxy_udp",
	"clustering_join",
	"proxy_tcp_udp_multi_port_handling",
	"network_state",
	"proxy_unix_dac_properties",
	"container_protection_delete",
	"unix_priv_drop",
	"pprof_http",
	"proxy_haproxy_protocol",
	"network_hwaddr",
	"proxy_nat",
	"network_nat_order",
	"container_full",
	"candid_authentication",
	"backup_compression",
	"candid_config",
	"nvidia_runtime_config",
	"storage_api_volume_snapshots",
	"storage_unmapped",
	"projects",
	"candid_config_key",
	"network_vxlan_ttl",
	"container_incremental_copy",
	"usb_optional_vendorid",
	"snapshot_scheduling",
	"container_copy_project",
	"clustering_server_address",
	"clustering_image_replication",
	"container_protection_shift",
	"snapshot_expiry",
	"container_backup_override_pool",
	"snapshot_expiry_creation",
	"network_leases_location",
	"resources_cpu_socket",
	"resources_gpu",
	"resources_numa",
	"kernel_features",
	"id_map_current",
	"event_location",
	"storage_api_remote_volume_snapshots",
	"network_nat_address",
	"container_nic_routes",
	"rbac",
	"cluster_internal_copy",
	"seccomp_notify",
	"lxc_features",
	"container_nic_ipvlan",
	"network_vlan_sriov",
	"storage_cephfs",
	"container_nic_ipfilter",
	"resources_v2",
	"container_exec_user_group_cwd",
	"container_syscall_intercept",
	"container_disk_shift",
	"storage_shifted",
	"resources_infiniband",
	"daemon_storage",
	"instances",
	"image_types",
	"resources_disk_sata",
	"clustering_roles",
	"images_expiry",
	"resources_network_firmware",
	"backup_compression_algorithm",
	"ceph_data_pool_name",
	"container_syscall_intercept_mount",
	"compression_squashfs",
	"container_raw_mount",
	"container_nic_routed",
	"container_syscall_intercept_mount_fuse",
	"container_disk_ceph",
	"virtual-machines",
	"unix_hotplug_devices",
}

// APIExtensionsCount returns the number of available API extensions.
func APIExtensionsCount() int {
	count := len(APIExtensions)

	// This environment variable is an internal one to force the code
	// to believe that we an API extensions version greater than we
	// actually have. It's used by integration tests to exercise the
	// cluster upgrade process.
	artificialBump := os.Getenv("LXD_ARTIFICIALLY_BUMP_API_EXTENSIONS")
	if artificialBump != "" {
		n, err := strconv.Atoi(artificialBump)
		if err == nil {
			count += n
		}
	}

	return count
}
