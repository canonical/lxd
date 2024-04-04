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
  echo "${list_output}" | grep -Fq 'server,/1.0,"admin,can_create_groups,can_create_identities,can_create_identity_provider_groups,can_create_projects,can_create_storage_pools,can_delete_groups,can_delete_identities,can_delete_identity_provider_groups,can_delete_projects,can_delete_storage_pools,can_edit,can_edit_groups,can_edit_identities,can_edit_identity_provider_groups,can_edit_projects,can_edit_storage_pools,can_override_cluster_target_restriction,can_view_groups,can_view_identities,can_view_identity_provider_groups,can_view_metrics,can_view_permissions,can_view_privileged_events,can_view_projects,can_view_resources,can_view_warnings,permission_manager,project_manager,storage_pool_manager,viewer"'

  list_output="$(lxc auth permission list entity_type=project --format csv --max-entitlements 0)"
  echo "${list_output}" | grep -Fq 'project,/1.0/projects/default,"can_create_image_aliases,can_create_images,can_create_instances,can_create_network_acls,can_create_network_zones,can_create_networks,can_create_profiles,can_create_storage_buckets,can_create_storage_volumes,can_delete,can_delete_image_aliases,can_delete_images,can_delete_instances,can_delete_network_acls,can_delete_network_zones,can_delete_networks,can_delete_profiles,can_delete_storage_buckets,can_delete_storage_volumes,can_edit,can_edit_image_aliases,can_edit_images,can_edit_instances,can_edit_network_acls,can_edit_network_zones,can_edit_networks,can_edit_profiles,can_edit_storage_buckets,can_edit_storage_volumes,can_operate_instances,can_view,can_view_events,can_view_image_aliases,can_view_images,can_view_instances,can_view_metrics,can_view_network_acls,can_view_network_zones,can_view_networks,can_view_operations,can_view_profiles,can_view_storage_buckets,can_view_storage_volumes,image_alias_manager,image_manager,instance_manager,network_acl_manager,network_manager,network_zone_manager,operator,profile_manager,storage_bucket_manager,storage_volume_manager,viewer"'

  # Test max entitlements flag doesn't apply to entitlements that are assigned.
  lxc auth group permission add test-group server viewer
  lxc auth group permission add test-group server project_manager
  list_output="$(lxc auth permission list entity_type=server --format csv)"
  echo "${list_output}" | grep -Fq 'server,/1.0,"project_manager:(test-group),viewer:(test-group),admin,can_create_groups,can_create_identities,..."'

  # Remove existing group permissions before testing fine-grained auth.
  lxc auth group permission remove test-group server viewer
  lxc auth group permission remove test-group server project_manager

  # Perform access checks
  fine_grained_authorization

  # Cleanup
  lxc auth group delete test-group
  lxc auth identity-provider-group delete test-idp-group
  lxc remote remove oidc
  kill_oidc
  rm "${TEST_DIR}/oidc.user"
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id
}


fine_grained_authorization() {
  echo "==> Checking permissions for member of group with no permissions..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
  user_is_not_project_operator

  # Give the test-group the `admin` entitlement on entity type `server`.
  lxc auth group permission add test-group server admin

  echo "==> Checking permissions for member of group with admin entitlement on server..."
  user_is_server_admin
  user_is_server_operator
  user_can_edit_projects
  user_is_project_operator

  # Give the test-group the `project_manager` entitlement on entity type `server`.
  lxc auth group permission remove test-group server admin
  lxc auth group permission add test-group server project_manager

  echo "==> Checking permissions for member of group with project_manager entitlement on server..."
  user_is_not_server_admin
  user_is_server_operator
  user_can_edit_projects
  user_is_project_operator

  # Give the test-group the `operator` entitlement on the default project.
  lxc auth group permission remove test-group server project_manager
  lxc auth group permission add test-group project default operator

  echo "==> Checking permissions for member of group with operator entitlement on default project..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
  user_is_project_operator

  lxc auth group permission remove test-group project default operator

  # Can't create a permission for an instance that doesn't exist.
  ! lxc auth group permission add test-group instance user-foo user project=default || false

  # Create an instance for testing the `user` entitlement on entity type `instance`.
  lxc launch testimage user-foo

  # Change permission to "user" for instance "user-foo"
  lxc auth group permission add test-group instance user-foo user project=default

  # To exec into an instance, the test-group will also need `can_view_events` for the project.
  # This is because the client uses the events API to figure out when the operation is finished.
  # Ideally we would use operations for this instead or allow more fine-grained filtering on events.
  lxc auth group permission add test-group project default can_view_events

  echo "==> Checking permissions for member of group with user entitlement on instance user-foo in default project..."
  user_is_instance_user user-foo # Pass instance name into test as we don't have permission to create one.
  lxc delete user-foo --force # Must clean this up now as subsequent tests assume a clean project.
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
  user_is_not_project_operator
}

user_is_not_server_admin() {
  # Can always see server info (type-bound public access https://openfga.dev/docs/modeling/public-access).
  lxc_remote info oidc: > /dev/null

  # Cannot see any config.
  ! lxc_remote info oidc: | grep -Fq 'core.https_address' || false

  # Cannot set any config.
  ! lxc_remote config set oidc: core.proxy_https=https://example.com || false

  # Should still be able to list storage pools but not be able to see any storage pool config or delete.
  [ "$(lxc_remote storage list oidc: -f csv | wc -l)" = 1 ]
  lxc_remote storage create test-pool dir
  ! lxc_remote storage set oidc:test-pool rsync.compression=true || false
  ! lxc_remote storage show oidc:test-pool | grep -Fq 'source:' || false
  ! lxc_remote storage delete oidc:test-pool || false
  lxc_remote storage delete test-pool

  # Should not be able to create a storage pool.
  ! lxc_remote storage create oidc:test dir || false

  # Should not be able to see certificates
  [ "$(lxc_remote config trust list oidc: -f csv | wc -l)" = 0 ]

  # Cannot edit certificates.
  fingerprint="$(lxc config trust list -f csv | cut -d, -f4)"
  ! lxc config trust show "${fingerprint}" | sed -e "s/restricted: false/restricted: true/" | lxc_remote config trust edit "oidc:${fingerprint}" || false
}

user_is_not_server_operator() {
  # Should not be able to create a project.
  ! lxc_remote project create oidc:new-project || false
}

user_is_server_admin() {
  # Should be able to see server config.
  lxc_remote info oidc: | grep -Fq 'core.https_address'

  # Should be able to add/remove certificates.
  gen_cert openfga-test
  test_cert_fingerprint="$(cert_fingerprint "${LXD_CONF}/openfga-test.crt")"
  certificate_add_token="$(lxc_remote config trust add oidc: --name test --quiet)"
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
  mv "${LXD_CONF}/openfga-test.crt" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/openfga-test.key" "${LXD_CONF}/client.key"
  lxc_remote remote add test-remote "${certificate_add_token}"
  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"
  lxc_remote config trust remove "oidc:${test_cert_fingerprint}"
  lxc_remote remote remove test-remote

  # Should be able to create/edit/delete a storage pool.
  lxc_remote storage create oidc:test-pool dir
  lxc_remote storage set oidc:test-pool rsync.compression=true
  lxc_remote storage show oidc:test-pool | grep -Fq 'rsync.compression:'
  lxc_remote storage delete oidc:test-pool
}

user_is_server_operator() {
  # Should be able to see projects.
  lxc_remote project list oidc: -f csv | grep -Fq 'default'

  # Should be able to create/edit/delete a project.
  lxc_remote project create oidc:test-project
  lxc_remote project show oidc:test-project | sed -e 's/description: ""/description: "Test Project"/' | lxc_remote project edit oidc:test-project
  lxc_remote project delete oidc:test-project
}

user_can_edit_projects() {
  lxc_remote project set oidc:default user.foo bar
  lxc_remote project unset oidc:default user.foo
}

user_is_not_project_manager() {
  ! lxc_remote project set oidc:default user.foo bar || false
  ! lxc_remote project unset oidc:default user.foo || false
}

user_is_project_operator() {
    # Should be able to create/edit/delete project level resources
    lxc_remote profile create oidc:test-profile
    lxc_remote profile device add oidc:test-profile eth0 none
    lxc_remote profile delete oidc:test-profile
    lxc_remote network create oidc:test-network
    lxc_remote network set oidc:test-network bridge.mtu=1500
    lxc_remote network delete oidc:test-network
    lxc_remote network acl create oidc:test-network-acl
    lxc_remote network acl delete oidc:test-network-acl
    lxc_remote network zone create oidc:test-network-zone
    lxc_remote network zone delete oidc:test-network-zone
    pool_name="$(lxc_remote storage list oidc: -f csv | cut -d, -f1)"
    lxc_remote storage volume create "oidc:${pool_name}" test-volume
    lxc_remote storage volume delete "oidc:${pool_name}" test-volume
    lxc_remote launch testimage oidc:operator-foo
    LXC_LOCAL='' lxc_remote exec oidc:operator-foo -- echo "bar"
    lxc_remote delete oidc:operator-foo --force
}

user_is_not_project_operator() {
  # Project list will not fail but there will be no output.
  [ "$(lxc project list oidc: -f csv | wc -l)" = 0 ]
  ! lxc project show oidc:default || false

  # Should not be able to see or create any instances.
  lxc_remote init testimage c1
  [ "$(lxc_remote list oidc: -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote list oidc: -f csv --all-projects | wc -l)" = 0 ]
  ! lxc_remote init testimage oidc:test-instance || false
  lxc_remote delete c1 -f

  # Should not be able to see network allocations.
  [ "$(lxc_remote network list-allocations oidc: -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote network list-allocations oidc: --all-projects -f csv | wc -l)" = 0 ]

  # Should not be able to see or create networks.
  [ "$(lxc_remote network list oidc: -f csv | wc -l)" = 0 ]
  ! lxc_remote network create oidc:test-network || false

  # Should not be able to see or create network ACLs.
  lxc_remote network acl create acl1
  [ "$(lxc_remote network acl list oidc: -f csv | wc -l)" = 0 ]
  ! lxc_remote network acl create oidc:test-acl || false
  lxc_remote network acl delete acl1

  # Should not be able to see or create network zones.
  lxc_remote network zone create zone1
  [ "$(lxc_remote network zone list oidc: -f csv | wc -l)" = 0 ]
  ! lxc_remote network zone create oidc:test-zone || false
  lxc_remote network zone delete zone1

  # Should not be able to see or create profiles.
  [ "$(lxc_remote profile list oidc: -f csv | wc -l)" = 0 ]
  ! lxc_remote profile create oidc:test-profile || false

  # Should not be able to see or create image aliases
  test_image_fingerprint="$(lxc_remote image info testimage | awk '/^Fingerprint/ {print $2}')"
  [ "$(lxc_remote image alias list oidc: -f csv | wc -l)" = 0 ]
  ! lxc_remote image alias create oidc:testimage2 "${test_image_fingerprint}" || false

  # Should not be able to see or create storage pool volumes.
  pool_name="$(lxc_remote storage list oidc: -f csv | cut -d, -f1)"
  lxc_remote storage volume create "${pool_name}" vol1
  [ "$(lxc_remote storage volume list "oidc:${pool_name}" -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote storage volume list "oidc:${pool_name}" --all-projects -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote storage volume list "oidc:" -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote storage volume list "oidc:" --all-projects -f csv | wc -l)" = 0 ]
  ! lxc_remote storage volume create "oidc:${pool_name}" test-volume || false
  lxc_remote storage volume delete "${pool_name}" vol1

  # Should not be able to see any operations.
  [ "$(lxc_remote operation list oidc: -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote operation list oidc: --all-projects -f csv | wc -l)" = 0 ]

  # Image list will still work but none will be shown because none are public.
  [ "$(lxc_remote image list oidc: -f csv | wc -l)" = 0 ]

  # Image edit will fail. Note that this fails with "not found" because we fail to resolve the alias (image is not public
  # so it is not returned from the DB).
  ! lxc_remote image set-property oidc:testimage requirements.secureboot true || false
  test_image_fingerprint_short="$(echo "${test_image_fingerprint}" | cut -c1-12)"
  ! lxc_remote image set-property "oidc:${test_image_fingerprint_short}" requirements.secureboot true || false

  # Should be able to list public images.
  lxc_remote image show testimage | sed -e "s/public: false/public: true/" | lxc_remote image edit testimage
  lxc_remote image list oidc: -f csv | grep -Fq "${test_image_fingerprint_short}"
  lxc_remote image show testimage | sed -e "s/public: true/public: false/" | lxc_remote image edit testimage
}

user_is_instance_user() {
  instance_name="${1}"

  # Check we can still interact with the instance.
  touch "${TEST_DIR}/tmp"
  lxc_remote file push "${TEST_DIR}/tmp" "oidc:${instance_name}/root/tmpfile.txt"
  LXC_LOCAL='' lxc_remote exec "oidc:${instance_name}" -- rm /root/tmpfile.txt
  rm "${TEST_DIR}/tmp"

  # We can't edit the instance though
  ! lxc_remote config set "oidc:${instance_name}" user.fizz=buzz || false
}
