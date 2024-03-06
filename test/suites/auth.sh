test_authorization() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"
  tls_user_fingerprint="$(lxc config trust list --format json | jq -r '.[0].fingerprint')"

  ### GROUP MANAGEMENT ###
  lxc auth group create test-group

  # Invalid entity types
  ! lxc auth group permission add test-group not_an_entity_type admin || false
  ! lxc auth group permission add test-group not_an_entity_type not_an_entity_name admin || false

  # Entity types with no entitlements
  ! lxc auth group permission add test-group container fake_name can_view || false # No entitlements defined for containers (use instance).
  ! lxc auth group permission add test-group certificate "${tls_user_fingerprint}" can_view || false # No entitlements defined for certificates (use identity).
  ! lxc auth group permission add test-group instance_backup fake_name can_view || false # No entitlements defined for instance backups (use can_manage_backups on parent instance).
  ! lxc auth group permission add test-group instance_snapshot fake_name can_view || false # No entitlements defined for instance snapshots (use can_manage_snapshots on parent instance).
  ! lxc auth group permission add test-group node fake_name can_view || false # No entitlements defined for cluster members (use server entitlements).
  ! lxc auth group permission add test-group operation fake_name can_view || false # No entitlements defined for operations (use operation secrets).
  ! lxc auth group permission add test-group storage_volume_backup fake_name can_view || false # No entitlements defined for storage volume backups (use can_manage_backups on parent volume).
  ! lxc auth group permission add test-group storage_volume_snapshot fake_name can_view || false # No entitlements defined for storage volume snapshots (use can_manage_snapshots on parent volume).
  ! lxc auth group permission add test-group warning fake_name can_view || false # No entitlements defined for warnings (may contain sensitive data, use server level entitlements).
  ! lxc auth group permission add test-group cluster_group fake_name can_view || false # No entitlements defined for cluster groups (use server entitlements).

  # Server permissions
  lxc auth group permission add test-group server admin # Valid
  lxc auth group permission remove test-group server admin # Valid
  ! lxc auth group permission remove test-group server admin || false # Permission already removed
  ! lxc auth group permission add test-group server not_a_server_entitlement || false # Invalid entitlement

  # Identity permissions.
  ! lxc auth group permission add test-group identity "${tls_user_fingerprint}" can_view || false # Missing authentication method
  lxc auth group permission add test-group identity "tls/${tls_user_fingerprint}" can_view # Valid
  lxc auth group permission remove test-group identity "tls/${tls_user_fingerprint}" can_view
  ! lxc auth group permission remove test-group identity "tls/${tls_user_fingerprint}" can_view || false # Already removed

  # Project permissions.
  ! lxc auth group permission add test-group project not-found operator # Not found
  lxc auth group permission add test-group project default operator # Valid
  lxc auth group permission remove test-group project default operator # Valid
  ! lxc auth group permission remove test-group project default operator || false # Already removed
  ! lxc auth group permission add test-group project default not_a_project_entitlement || false # Invalid entitlement

  # Instance permissions.
  ! lxc auth group permission add test-group instance c1 can_exec project=default || false # Not found
  lxc init testimage c1
  ! lxc auth group permission add test-group instance c1 can_exec || false # No project
  lxc auth group permission add test-group instance c1 can_exec project=default # Valid
  lxc auth group permission remove test-group instance c1 can_exec project=default # Valid
  ! lxc auth group permission remove test-group instance c1 can_exec project=default || false # Already removed
  ! lxc auth group permission add test-group instance c1 not_an_instance_entitlement project=default || false # Invalid entitlement

  # Test permission is removed automatically when instance is removed.
  lxc auth group permission add test-group instance c1 can_exec project=default # Valid
  lxc rm c1 --force
  ! lxd sql global "SELECT * FROM auth_groups_permissions WHERE entitlement = 'can_exec'" | grep c1 || false # Permission should be removed when instance is removed.

  # Network permissions
  ! lxc auth group permission add test-group network n1 can_view project=default || false # Not found
  lxc network create n1
  ! lxc auth group permission add test-group network n1 can_view || false # No project
  lxc auth group permission add test-group network n1 can_view project=default # Valid
  lxc auth group permission remove test-group network n1 can_view project=default # Valid
  ! lxc auth group permission remove test-group network n1 can_view project=default || false # Already removed
  ! lxc auth group permission add test-group network n1 not_a_network_entitlement project=default || false # Invalid entitlement
  lxc network rm n1

  ### IDENTITY MANAGEMENT ###
  lxc config trust show "${tls_user_fingerprint}"
  ! lxc auth identity group add "tls/${tls_user_fingerprint}" test-group || false # TLS identities cannot be added to groups (yet).

  spawn_oidc
  lxc config set "oidc.issuer=http://127.0.0.1:$(cat "${TEST_DIR}/oidc.port")/"
  lxc config set "oidc.client.id=device"

  set_oidc test-user test-user@example.com
  BROWSER=curl lxc remote add --accept-certificate oidc "${LXD_ADDR}" --auth-type oidc

  ! lxc auth identity group add oidc/test-user@example.com not-found || false # Group not found
  lxc auth identity group add oidc/test-user@example.com test-group # Valid

  # Check user has been added to the group.
  lxc auth identity list --format csv | grep -Fq 'oidc,OIDC client," ",test-user@example.com,test-group'

  # Test `lxc auth identity info`
  expected=$(cat << EOF
groups:
- test-group
authentication_method: oidc
type: OIDC client
id: test-user@example.com
name: ' '
effective_groups:
- test-group
effective_permissions: []
EOF
)
  lxc auth identity info oidc: | grep -Fz "${expected}"

  ### IDENTITY PROVIDER GROUP MANAGEMENT ###
  lxc auth identity-provider-group create test-idp-group
  ! lxc auth identity-provider-group group add test-idp-group not-found || false # Group not found
  lxc auth identity-provider-group group add test-idp-group test-group
  lxc auth identity-provider-group group remove test-idp-group test-group
  ! lxc auth identity-provider-group group remove test-idp-group test-group || false # Group not mapped

  ### PERMISSION INSPECTION ###
  list_output="$(lxc auth permission list --format csv)"

  # grep for some easily grepable things.
  echo "${list_output}" | grep -Fq 'identity,/1.0/auth/identities/oidc/test-user@example.com,"can_delete,can_edit,can_view"'
  echo "${list_output}" | grep -Fq 'group,/1.0/auth/groups/test-group,"can_delete,can_edit,can_view"'
  echo "${list_output}" | grep -Fq 'identity_provider_group,/1.0/auth/identity-provider-groups/test-idp-group,"can_delete,can_edit,can_view"'
  echo "${list_output}" | grep -Fq 'image_alias,/1.0/images/aliases/testimage?project=default,"can_delete,can_edit,can_view"'
  echo "${list_output}" | grep -Fq 'profile,/1.0/profiles/default?project=default,"can_delete,can_edit,can_view"'
  echo "${list_output}" | grep -Fq 'project,/1.0/projects/default,"can_create_image_aliases,can_create_images,can_create_instances,..."'

  list_output="$(lxc auth permission list entity_type=server --format csv --max-entitlements 0)"
  echo "${list_output}" | grep -Fq 'server,/1.0,"admin,can_create_groups,can_create_identities,can_create_projects,can_create_storage_pools,can_delete_groups,can_delete_identities,can_delete_projects,can_delete_storage_pools,can_edit,can_edit_groups,can_edit_identities,can_edit_projects,can_edit_storage_pools,can_override_cluster_target_restriction,can_view,can_view_configuration,can_view_groups,can_view_identities,can_view_metrics,can_view_permissions,can_view_privileged_events,can_view_projects,can_view_resources,can_view_warnings,permission_manager,project_manager,storage_pool_manager,viewer"'

  list_output="$(lxc auth permission list entity_type=project --format csv --max-entitlements 0)"
  echo "${list_output}" | grep -Fq 'project,/1.0/projects/default,"can_create_image_aliases,can_create_images,can_create_instances,can_create_network_acls,can_create_network_zones,can_create_networks,can_create_profiles,can_create_storage_buckets,can_create_storage_volumes,can_delete,can_delete_image_aliases,can_delete_images,can_delete_instances,can_delete_network_acls,can_delete_network_zones,can_delete_networks,can_delete_profiles,can_delete_storage_buckets,can_delete_storage_volumes,can_edit,can_edit_image_aliases,can_edit_images,can_edit_instances,can_edit_network_acls,can_edit_network_zones,can_edit_networks,can_edit_profiles,can_edit_storage_buckets,can_edit_storage_volumes,can_operate_instances,can_view,can_view_events,can_view_image_aliases,can_view_images,can_view_instances,can_view_network_acls,can_view_network_zones,can_view_networks,can_view_operations,can_view_profiles,can_view_storage_buckets,can_view_storage_volumes,image_alias_manager,image_manager,instance_manager,network_acl_manager,network_manager,network_zone_manager,operator,profile_manager,storage_bucket_manager,storage_volume_manager,viewer"'

  # Test max entitlements flag doesn't apply to entitlements that are assigned.
  lxc auth group permission add test-group server viewer
  lxc auth group permission add test-group server project_manager
  list_output="$(lxc auth permission list entity_type=server --format csv)"
  echo "${list_output}" | grep -Fq 'server,/1.0,"project_manager:(test-group),viewer:(test-group),admin,can_create_groups,can_create_identities,..."'

  # Cleanup
  lxc auth group delete test-group
  lxc auth identity-provider-group delete test-idp-group
  lxc remote remove oidc
  kill_oidc
  rm "${TEST_DIR}/oidc.user"
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id
}
