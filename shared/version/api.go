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
	"backup_compression",
	"nvidia_runtime_config",
	"storage_api_volume_snapshots",
	"storage_unmapped",
	"projects",
	"network_vxlan_ttl",
	"container_incremental_copy",
	"usb_optional_vendorid",
	"snapshot_scheduling",
	"snapshot_schedule_aliases",
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
	"image_profiles",
	"clustering_architecture",
	"resources_disk_id",
	"storage_lvm_stripes",
	"vm_boot_priority",
	"unix_hotplug_devices",
	"api_filtering",
	"instance_nic_network",
	"clustering_sizing",
	"firewall_driver",
	"projects_limits",
	"container_syscall_intercept_hugetlbfs",
	"limits_hugepages",
	"container_nic_routed_gateway",
	"projects_restrictions",
	"custom_volume_snapshot_expiry",
	"volume_snapshot_scheduling",
	"trust_ca_certificates",
	"snapshot_disk_usage",
	"clustering_edit_roles",
	"container_nic_routed_host_address",
	"container_nic_ipvlan_gateway",
	"resources_usb_pci",
	"resources_cpu_threads_numa",
	"resources_cpu_core_die",
	"api_os",
	"container_nic_routed_host_table",
	"container_nic_ipvlan_host_table",
	"container_nic_ipvlan_mode",
	"resources_system",
	"images_push_relay",
	"network_dns_search",
	"container_nic_routed_limits",
	"instance_nic_bridged_vlan",
	"network_state_bond_bridge",
	"usedby_consistency",
	"custom_block_volumes",
	"clustering_failure_domains",
	"resources_gpu_mdev",
	"console_vga_type",
	"projects_limits_disk",
	"network_type_macvlan",
	"network_type_sriov",
	"container_syscall_intercept_bpf_devices",
	"network_type_ovn",
	"projects_networks",
	"projects_networks_restricted_uplinks",
	"custom_volume_backup",
	"backup_override_name",
	"storage_rsync_compression",
	"network_type_physical",
	"network_ovn_external_subnets",
	"network_ovn_nat",
	"network_ovn_external_routes_remove",
	"tpm_device_type",
	"storage_zfs_clone_copy_rebase",
	"gpu_mdev",
	"resources_pci_iommu",
	"resources_network_usb",
	"resources_disk_address",
	"network_physical_ovn_ingress_mode",
	"network_ovn_dhcp",
	"network_physical_routes_anycast",
	"projects_limits_instances",
	"network_state_vlan",
	"instance_nic_bridged_port_isolation",
	"instance_bulk_state_change",
	"network_gvrp",
	"instance_pool_move",
	"gpu_sriov",
	"pci_device_type",
	"storage_volume_state",
	"network_acl",
	"migration_stateful",
	"disk_state_quota",
	"storage_ceph_features",
	"projects_compression",
	"projects_images_remote_cache_expiry",
	"certificate_project",
	"network_ovn_acl",
	"projects_images_auto_update",
	"projects_restricted_cluster_target",
	"images_default_architecture",
	"network_ovn_acl_defaults",
	"gpu_mig",
	"project_usage",
	"network_bridge_acl",
	"warnings",
	"projects_restricted_backups_and_snapshots",
	"clustering_join_token",
	"clustering_description",
	"server_trusted_proxy",
	"clustering_update_cert",
	"storage_api_project",
	"server_instance_driver_operational",
	"server_supported_storage_drivers",
	"event_lifecycle_requestor_address",
	"resources_gpu_usb",
	"clustering_evacuation",
	"network_ovn_nat_address",
	"network_bgp",
	"network_forward",
	"custom_volume_refresh",
	"network_counters_errors_dropped",
	"metrics",
	"image_source_project",
	"clustering_config",
	"network_peer",
	"linux_sysctl",
	"network_dns",
	"ovn_nic_acceleration",
	"certificate_self_renewal",
	"instance_project_move",
	"storage_volume_project_move",
	"cloud_init",
	"network_dns_nat",
	"database_leader",
	"instance_all_projects",
	"clustering_groups",
	"ceph_rbd_du",
	"instance_get_full",
	"qemu_metrics",
	"gpu_mig_uuid",
	"event_project",
	"clustering_evacuation_live",
	"instance_allow_inconsistent_copy",
	"network_state_ovn",
	"storage_volume_api_filtering",
	"image_restrictions",
	"storage_zfs_export",
	"network_dns_records",
	"storage_zfs_reserve_space",
	"network_acl_log",
	"storage_zfs_blocksize",
	"metrics_cpu_seconds",
	"instance_snapshot_never",
	"certificate_token",
	"instance_nic_routed_neighbor_probe",
	"event_hub",
	"agent_nic_config",
	"projects_restricted_intercept",
	"metrics_authentication",
	"images_target_project",
	"cluster_migration_inconsistent_copy",
	"cluster_ovn_chassis",
	"container_syscall_intercept_sched_setscheduler",
	"storage_lvm_thinpool_metadata_size",
	"storage_volume_state_total",
	"instance_file_head",
	"instances_nic_host_name",
	"image_copy_profile",
	"container_syscall_intercept_sysinfo",
	"clustering_evacuation_mode",
	"resources_pci_vpd",
	"qemu_raw_conf",
	"storage_cephfs_fscache",
	"network_load_balancer",
	"vsock_api",
	"instance_ready_state",
	"network_bgp_holdtime",
	"storage_volumes_all_projects",
	"metrics_memory_oom_total",
	"storage_buckets",
	"storage_buckets_create_credentials",
	"metrics_cpu_effective_total",
	"projects_networks_restricted_access",
	"storage_buckets_local",
	"loki",
	"acme",
	"internal_metrics",
	"cluster_join_token_expiry",
	"remote_token_expiry",
	"init_preseed",
	"storage_volumes_created_at",
	"cpu_hotplug",
	"projects_networks_zones",
	"network_txqueuelen",
	"cluster_member_state",
	"instances_placement_scriptlet",
	"storage_pool_source_wipe",
	"zfs_block_mode",
	"instance_generation_id",
	"disk_io_cache",
	"amd_sev",
	"storage_pool_loop_resize",
	"migration_vm_live",
	"ovn_nic_nesting",
	"oidc",
	"network_ovn_l3only",
	"ovn_nic_acceleration_vdpa",
	"cluster_healing",
	"instances_state_total",
	"auth_user",
	"security_csm",
	"instances_rebuild",
	"numa_cpu_placement",
	"custom_volume_iso",
	"network_allocations",
	"storage_api_remote_volume_snapshot_copy",
	"zfs_delegate",
	"operations_get_query_all_projects",
	"metadata_configuration",
	"syslog_socket",
	"event_lifecycle_name_and_project",
	"instances_nic_limits_priority",
	"disk_initial_volume_configuration",
	"operation_wait",
	"cluster_internal_custom_volume_copy",
	"disk_io_bus",
	"storage_cephfs_create_missing",
	"instance_move_config",
	"ovn_ssl_config",
	"init_preseed_storage_volumes",
	"metrics_instances_count",
	"server_instance_type_info",
	"resources_disk_mounted",
	"server_version_lts",
	"oidc_groups_claim",
	"loki_config_instance",
	"storage_volatile_uuid",
	"import_instance_devices",
	"instances_uefi_vars",
	"instances_migration_stateful",
	"container_syscall_filtering_allow_deny_syntax",
	"access_management",
	"vm_disk_io_limits",
	"storage_volumes_all",
	"instances_files_modify_permissions",
	"image_restriction_nesting",
	"container_syscall_intercept_finit_module",
	"device_usb_serial",
	"network_allocate_external_ips",
	"explicit_trust_token",
	"instance_import_conversion",
	"instance_create_start",
	"devlxd_images_vm",
	"instance_protection_start",
	"disk_io_bus_virtio_blk",
	"ubuntu_pro_guest_attach",
	"metadata_configuration_entity_types",
	"network_allocations_ovn_uplink",
	"network_ovn_uplink_vlan",
	"shared_custom_block_volumes",
	"metrics_api_requests",
	"projects_limits_disk_pool",
	"access_management_tls",
	"state_logical_cpus",
}

// APIExtensionsCount returns the number of available API extensions.
func APIExtensionsCount() int {
	count := len(APIExtensions)

	// This environment variable is an internal one to force the code
	// to believe that we have an API extensions count greater than we
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
