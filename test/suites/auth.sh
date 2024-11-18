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

  # Certificate permissions.
  ! lxc auth group permission add test-group certificate notacertificate can_view || false # Not found
  lxc auth group permission add test-group certificate "${tls_user_fingerprint}" can_view # Valid
  lxc auth group permission remove test-group certificate "${tls_user_fingerprint}" can_view
  ! lxc auth group permission remove test-group certificate "${tls_user_fingerprint}" can_view || false # Already removed
  ! lxc auth group permission add test-group identity "tls/${tls_user_fingerprint}" can_view || false # The certificate is not an identity, it is a certificate.

  # Project permissions.
  ! lxc auth group permission add test-group project not-found operator || false # Not found
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

  # Test fine-grained TLS identity creation
  tls_identity_token="$(lxc auth identity create tls/test-user --quiet --group test-group)"
  LXD_CONF2=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF2}" gen_cert_and_key "client"

  # Cannot use the token with the certificates API and the correct error is returned.
  [ "$(LXD_CONF="${LXD_CONF2}" my_curl -X POST "https://${LXD_ADDR}/1.0/certificates" --data "{\"trust_token\": \"${tls_identity_token}\"}" | jq -er '.error')" = "Failed during search for certificate add token operation: TLS Identity token detected (you must update your client)" ]

  # Can use the token with remote add command.
  LXD_CONF="${LXD_CONF2}" lxc remote add tls "${tls_identity_token}"
  [ "$(LXD_CONF="${LXD_CONF2}" lxc_remote query tls:/1.0 | jq -r '.auth')" = 'trusted' ]

  # Check a token cannot be used when expired
  lxc config set core.remote_token_expiry=1S
  tls_identity_token2="$(lxc auth identity create tls/test-user2 --quiet)"
  sleep 2
  LXD_CONF3=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF3}" gen_cert_and_key "client"
  ! LXD_CONF="${LXD_CONF3}" lxc remote add tls "${tls_identity_token2}" || false

  # The token was used, so the pending identity should be deleted.
  [ "$(lxc auth identity list --format csv | grep -cF 'pending')" = 0 ]

  # Check token prune task works
  lxc auth identity create tls/test-user2 --quiet
  [ "$(lxc auth identity list --format csv | grep -cF 'pending')" = 1 ]
  sleep 2 # Wait for token to expire (expiry is still set to 1 second)
  lxc query --request POST /internal/testing/prune-tokens
  [ "$(lxc auth identity list --format csv | grep -cF 'pending')" = 0 ]

  # Check users have been added to the group.
  tls_identity_fingerprint="$(cert_fingerprint "${LXD_CONF2}/client.crt")"
  lxc auth identity list --format csv | grep -Fq 'oidc,OIDC client," ",test-user@example.com,test-group'
  lxc auth identity list --format csv | grep -Fq "tls,Client certificate,test-user,${tls_identity_fingerprint},test-group"

  # Test `lxc auth identity info`
  expectedOIDCInfo='authentication_method: oidc
type: OIDC client
id: test-user@example.com
name: '"'"' '"'"'
groups:
- test-group
tls_certificate: ""
effective_groups:
- test-group
effective_permissions: []'
  [ "$(lxc auth identity info oidc:)" = "${expectedOIDCInfo}" ]

  expectedTLSInfo="authentication_method: tls
type: Client certificate
id: ${tls_identity_fingerprint}
name: test-user
groups:
- test-group
tls_certificate: |
$(awk '{printf "  %s\n", $0}' "${LXD_CONF2}/client.crt")
effective_groups:
- test-group
effective_permissions: []"
  [ "$(LXD_CONF="${LXD_CONF2}" lxc auth identity info tls:)" = "${expectedTLSInfo}" ]


  # Identity permissions.
  ! lxc auth group permission add test-group identity test-user@example.com can_view || false # Missing authentication method
  lxc auth group permission add test-group identity oidc/test-user@example.com can_view # Valid
  lxc auth group permission remove test-group identity oidc/test-user@example.com can_view
  ! lxc auth group permission remove test-group identity oidc/test-user@example.com can_view || false # Already removed

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
  echo "${list_output}" | grep -Fq 'server,/1.0,"admin,can_create_groups,can_create_identities,can_create_identity_provider_groups,can_create_projects,can_create_storage_pools,can_delete_groups,can_delete_identities,can_delete_identity_provider_groups,can_delete_projects,can_delete_storage_pools,can_edit,can_edit_groups,can_edit_identities,can_edit_identity_provider_groups,can_edit_projects,can_edit_storage_pools,can_override_cluster_target_restriction,can_view_groups,can_view_identities,can_view_identity_provider_groups,can_view_metrics,can_view_permissions,can_view_privileged_events,can_view_projects,can_view_resources,can_view_unmanaged_networks,can_view_warnings,permission_manager,project_manager,storage_pool_manager,viewer"'

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

  storage_pool_used_by "oidc"
  LXD_CONF="${LXD_CONF2}" storage_pool_used_by "tls"

  # Perform access checks
  fine_grained_authorization "oidc"
  LXD_CONF="${LXD_CONF2}" fine_grained_authorization "tls"

  # Perform access check compatibility with project feature flags
  auth_project_features "oidc"
  LXD_CONF="${LXD_CONF2}" auth_project_features "tls"

  # The OIDC identity should be able to delete themselves without any permissions.
  lxc auth identity group remove oidc/test-user@example.com test-group
  lxc_remote auth identity info oidc: | grep -Fq 'effective_permissions: []'
  lxc_remote auth identity delete oidc:oidc/test-user@example.com
  ! lxc auth identity list --format csv | grep -Fq 'test-user@example.com' || false

  # When the OIDC identity re-authenticates they should reappear in the database
  [ "$(lxc_remote query oidc:/1.0 | jq -r '.auth')" = "trusted" ]
  lxc auth identity list --format csv | grep -Fq 'test-user@example.com'
  lxc_remote auth identity info oidc: | grep -Fq 'effective_permissions: []'

  # The OIDC identity cannot see or delete the TLS identity.
  ! lxc_remote auth identity show "oidc:tls/${tls_identity_fingerprint}" || false
  ! lxc_remote auth identity delete "oidc:tls/${tls_identity_fingerprint}" || false

  # But the TLS identity can see and delete itself
  LXD_CONF="${LXD_CONF2}" lxc_remote auth identity list tls: --format csv | grep -Fq "${tls_identity_fingerprint}"
  LXD_CONF="${LXD_CONF2}" lxc_remote auth identity delete "tls:tls/${tls_identity_fingerprint}"
  ! lxc auth identity list --format csv | grep -Fq "${tls_identity_fingerprint}" || false

  # The TLS identity is not trusted after deletion.
  [ "$(LXD_CONF="${LXD_CONF2}" lxc_remote query tls:/1.0 | jq -r '.auth')" = "untrusted" ]

  # Check a TLS identity can update their own certificate.
  # First create a new TLS identity and add it to test-group
  LXD_CONF4=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF4}" gen_cert_and_key "client"
  token="$(lxc auth identity create tls/test-user4 --quiet)"
  LXD_CONF="${LXD_CONF4}" lxc_remote remote add tls "${token}"
  lxc auth identity group add tls/test-user4 test-group

  # Create another certificate to update to
  LXD_CONF5=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF5}" gen_cert_and_key "client"

  # We're using my_curl because the lxc wrapper function splits the --data argument on the spaces between "BEGIN CERTIFICATE" and lxc query returns a usage error.
  # We could use lxc edit as it accepts stdin input, but replacing the certificate in the yaml was quite complicated.

  # This asserts that test-user4 cannot change their own group membership
  [ "$(LXD_CONF="${LXD_CONF4}" my_curl "https://${LXD_ADDR}/1.0/auth/identities/tls/test-user4" -X PUT --data "{\"tls_certificate\":\"$(awk '{printf "%s\\n", $0}' "${LXD_CONF5}/client.crt")\"}" | jq -r '.error_code')" -eq 403 ]

  # This asserts that test-user4 can change their own certificate as long as the groups are unchanged
  [ "$(LXD_CONF="${LXD_CONF4}" my_curl "https://${LXD_ADDR}/1.0/auth/identities/tls/test-user4" -X PUT --data "{\"tls_certificate\":\"$(awk '{printf "%s\\n", $0}' "${LXD_CONF5}/client.crt")\", \"groups\":[\"test-group\"]}" | jq -r '.status_code')" -eq 200 ]

  # The original certificate is untrusted after the update
  [ "$(LXD_CONF="${LXD_CONF4}" lxc_remote query tls:/1.0 | jq -r '.auth')" = "untrusted" ]

  # Add the remote for the lxc config directory with the other certificates. No token needed as we're already trusted.
  LXD_CONF="${LXD_CONF5}" lxc remote add tls "${LXD_ADDR}" --accept-certificate --auth-type tls
  [ "$(LXD_CONF="${LXD_CONF5}" lxc_remote query tls:/1.0 | jq -r '.auth')" = "trusted" ]

  # Do the same tests with patch. test-user4 cannot change their group membership
  [ "$(LXD_CONF="${LXD_CONF5}" my_curl "https://${LXD_ADDR}/1.0/auth/identities/tls/test-user4" -X PATCH --data "{\"tls_certificate\":\"$(awk '{printf "%s\\n", $0}' "${LXD_CONF4}/client.crt")\", \"groups\":[\"new-group\"]}" | jq -r '.error_code')" -eq 403 ]

  # Change the certificate back to the original, using patch. Here no groups are in the request, only the certificate.
  [ "$(LXD_CONF="${LXD_CONF5}" my_curl "https://${LXD_ADDR}/1.0/auth/identities/tls/test-user4" -X PATCH --data "{\"tls_certificate\":\"$(awk '{printf "%s\\n", $0}' "${LXD_CONF4}/client.crt")\"}" | jq -r '.status_code')" -eq 200 ]
  [ "$(LXD_CONF="${LXD_CONF4}" lxc_remote query tls:/1.0 | jq -r '.auth')" = "trusted" ]
  [ "$(LXD_CONF="${LXD_CONF5}" lxc_remote query tls:/1.0 | jq -r '.auth')" = "untrusted" ]

  # Cleanup
  lxc auth group delete test-group
  lxc auth identity-provider-group delete test-idp-group
  lxc remote remove oidc
  kill_oidc
  rm "${TEST_DIR}/oidc.user"
  rm -r "${LXD_CONF2}"
  rm -r "${LXD_CONF3}"
  rm -r "${LXD_CONF4}"
  rm -r "${LXD_CONF5}"
  lxc config unset core.remote_token_expiry
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id
}


storage_pool_used_by() {
  remote="${1}"

  # test-group must have no permissions to start the test.
  [ "$(lxc query /1.0/auth/groups/test-group | jq '.permissions | length')" -eq 0 ]

  # Test storage pool used-by filtering
  pool_name="$(lxc storage list -f csv | cut -d, -f1)"

  # Used-by list should have only the default profile, but in case of any leftover entries from previous tests get a
  # start size for the list and work against that.
  start_length=$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')

  # Members of test-group have no permissions, so they should get an empty list.
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 0 ]

  # Launch instance. Should appear in pool used-by list. Members of test-group still can't see anything.
  lxc launch testimage c1
  [ "$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq $((start_length+1)) ]
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 0 ]

  # Allow members of test-group to view the instance. They should see it in the used-by list.
  lxc auth group permission add test-group instance c1 can_view project=default
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 1 ]

  # Take a snapshot. Used-by length should increase. Members of test-group should see the snapshot.
  lxc snapshot c1
  [ "$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq $((start_length+2)) ]
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 2 ]

  # Take another snapshot and check again. This is done because filtering used-by lists takes a slightly different code
  # path when it receives multiple URLs of the same entity type.
  lxc snapshot c1
  [ "$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq $((start_length+3)) ]
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 3 ]

  # Perform the same checks with storage volume snapshots.
  lxc storage volume create "${pool_name}" vol1
  [ "$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq $((start_length+4)) ]
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 3 ]

  lxc auth group permission add test-group storage_volume vol1 can_view project=default pool="${pool_name}" type=custom
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 4 ]

  lxc storage volume snapshot "${pool_name}" vol1
  [ "$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq $((start_length+5)) ]
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 5 ]

  lxc storage volume snapshot "${pool_name}" vol1
  [ "$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq $((start_length+6)) ]
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 6 ]

  # Remove can_view on the volume and check the volume and snapshots are no longer in the used-by list.
  lxc auth group permission remove test-group storage_volume vol1 can_view project=default pool="${pool_name}" type=custom
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 3 ]

  # Remove can_view on the instance and check the volume and snapshots are no longer in the used-by list.
  lxc auth group permission remove test-group instance c1 can_view project=default
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq 0 ]

  # Clean up storage volume used-by tests.
  lxc delete c1 -f
  lxc storage volume delete "${pool_name}" vol1
  [ "$(lxc query "/1.0/storage-pools/${pool_name}" | jq '.used_by | length')" -eq $((start_length)) ]
}

fine_grained_authorization() {
  remote="${1}"

  echo "==> Checking permissions for member of group with no permissions..."
  user_is_not_server_admin "${remote}"
  user_is_not_server_operator "${remote}"
  user_is_not_project_manager "${remote}"
  user_is_not_project_operator "${remote}"

  # Give the test-group the `admin` entitlement on entity type `server`.
  lxc auth group permission add test-group server admin

  echo "==> Checking permissions for member of group with admin entitlement on server..."
  user_is_server_admin "${remote}"
  user_is_server_operator "${remote}"
  user_can_edit_projects "${remote}"
  user_is_project_operator "${remote}"

  # Give the test-group the `project_manager` entitlement on entity type `server`.
  lxc auth group permission remove test-group server admin
  lxc auth group permission add test-group server project_manager

  echo "==> Checking permissions for member of group with project_manager entitlement on server..."
  user_is_not_server_admin "${remote}"
  user_is_server_operator "${remote}"
  user_can_edit_projects "${remote}"
  user_is_project_operator "${remote}"

  # Give the test-group the `operator` entitlement on the default project.
  lxc auth group permission remove test-group server project_manager
  lxc auth group permission add test-group project default operator

  echo "==> Checking permissions for member of group with operator entitlement on default project..."
  user_is_not_server_admin "${remote}"
  user_is_not_server_operator "${remote}"
  user_is_not_project_manager "${remote}"
  user_is_project_operator "${remote}"

  lxc auth group permission remove test-group project default operator

  # Can't create a permission for an instance that doesn't exist.
  ! lxc auth group permission add test-group instance user-foo user project=default || false

  # Create an instance for testing the `user` entitlement on entity type `instance`.
  lxc launch testimage user-foo

  # Change permission to "user" for instance "user-foo"
  lxc auth group permission add test-group instance user-foo user project=default

  # To exec into an instance, Members of test-group will also need `can_view_events` for the project.
  # This is because the client uses the events API to figure out when the operation is finished.
  # Ideally we would use operations for this instead or allow more fine-grained filtering on events.
  lxc auth group permission add test-group project default can_view_events

  echo "==> Checking permissions for member of group with user entitlement on instance user-foo in default project..."
  user_is_instance_user "${remote}" user-foo # Pass instance name into test as we don't have permission to create one.
  lxc delete user-foo --force # Must clean this up now as subsequent tests assume a clean project.
  user_is_not_server_admin "${remote}"
  user_is_not_server_operator "${remote}"
  user_is_not_project_manager "${remote}"
  user_is_not_project_operator "${remote}"

  lxc auth group permission remove test-group project default can_view_events

  echo "==> Checking 'can_view_warnings' entitlement..."
  # Delete previous warnings
  lxc query --wait /1.0/warnings\?recursion=1 | jq -r '.[].uuid' | xargs -n1 lxc warning delete

  # Create a global warning (no node and no project)
  lxc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"authorization warning\"}' /internal/testing/warnings

  # Check we are not able to view warnings currently
  ! lxc_remote warning list "${remote}:" || false

  # Add "can_view_warnings" permission to group.
  lxc auth group permission add test-group server can_view_warnings

  # Check we can view the warning we just created.
  [ "$(lxc_remote query "${remote}:/1.0/warnings?recursion=1" | jq -r '[.[] | select(.last_message == "authorization warning")] | length')" = 1 ]

  lxc auth group permission remove test-group server can_view_warnings

  # Check we are not able to view any server config currently.
  # Here we explicitly a setting that contains an actual password.
  lxc config set loki.auth.password bar
  [ "$(lxc_remote query "${remote}:/1.0" | jq '.config | length')" = 0 ]
  [ "$(lxc_remote query "${remote}:/1.0" | jq -r '.config."loki.auth.password"')" = "null" ]

  # Check we are not able to set any server config currently.
  ! lxc_remote config set "${remote}:" loki.auth.password bar2 || false

  # Add "can_edit" permission to group.
  lxc auth group permission add test-group server can_edit

  # Check we can view the server's config.
  [ "$(lxc_remote query "${remote}:/1.0" | jq -r '.config."loki.auth.password"')" = "bar" ]

  # Check we can modify the server's config.
  lxc_remote config set "${remote}:" loki.auth.password bar2

  lxc auth group permission remove test-group server can_edit
  lxc config unset loki.auth.password

  # Check we are not able to view any storage pool config currently.
  lxc storage create test-pool dir
  lxc storage set test-pool user.foo bar
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/test-pool" | jq '.config | length')" = 0 ]
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/test-pool" | jq -r '.config."user.foo"')" = "null" ]

  # Add "can_edit" permission to storage pool.
  lxc auth group permission add test-group storage_pool test-pool can_edit

  # Check we can view the storage pool's config.
  [ "$(lxc_remote query "${remote}:/1.0/storage-pools/test-pool" | jq -r '.config."user.foo"')" = "bar" ]

  lxc auth group permission remove test-group storage_pool test-pool can_edit
  lxc storage delete test-pool
}

user_is_not_server_admin() {
  remote="${1}"

  # Can always see server info (type-bound public access https://openfga.dev/docs/modeling/public-access).
  lxc_remote info "${remote}:" > /dev/null

  # Cannot see any config.
  ! lxc_remote info "${remote}:" | grep -Fq 'core.https_address' || false

  # Cannot set any config.
  ! lxc_remote config set "${remote}:" core.proxy_https=https://example.com || false

  # Should still be able to list storage pools but not be able to see any storage pool config or delete.
  [ "$(lxc_remote storage list "${remote}:" -f csv | wc -l)" = 1 ]
  lxc_remote storage create test-pool dir
  ! lxc_remote storage set "${remote}:test-pool" rsync.compression=true || false
  ! lxc_remote storage show "${remote}:test-pool" | grep -Fq 'source:' || false
  ! lxc_remote storage delete "${remote}:test-pool" || false
  lxc_remote storage delete test-pool

  # Should not be able to create a storage pool.
  ! lxc_remote storage create "${remote}:test" dir || false

  # Should not be able to see certificates
  [ "$(lxc_remote config trust list "${remote}:" -f csv | wc -l)" = 0 ]

  # Cannot edit certificates.
  fingerprint="$(lxc config trust list -f csv | cut -d, -f4)"
  ! lxc config trust show "${fingerprint}" | sed -e "s/restricted: false/restricted: true/" | lxc_remote config trust edit "${remote}:${fingerprint}" || false
}

user_is_not_server_operator() {
  remote="${1}"

  # Should not be able to create a project.
  ! lxc_remote project create "${remote}:new-project" || false
}

user_is_server_admin() {
  remote="${1}"

  # Should be able to see server config.
  lxc_remote info "${remote}:" | grep -Fq 'core.https_address'

  ## Should be able to add/remove certificates.
  # Create a temporary lxc config directory with some certs to test with.
  TMP_LXD_CONF=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${TMP_LXD_CONF}" gen_cert_and_key client
  tmp_cert_fingerprint="$(cert_fingerprint "${TMP_LXD_CONF}/client.crt")"

  # Can get a certificate add token as a server administrator.
  certificate_add_token="$(lxc_remote config trust add "${remote}:" --name test --quiet)"

  # The token works.
  LXD_CONF="${TMP_LXD_CONF}" lxc_remote remote add test-remote "${certificate_add_token}"

  # Clean up test certificate and config dir.
  lxc_remote config trust remove "${remote}:${tmp_cert_fingerprint}"
  rm -r "${TMP_LXD_CONF}"

  ## Should be able to create/edit/delete a storage pool.
  lxc_remote storage create "${remote}:test-pool" dir
  lxc_remote storage set "${remote}:test-pool" rsync.compression=true
  lxc_remote storage show "${remote}:test-pool" | grep -Fq 'rsync.compression:'
  lxc_remote storage delete "${remote}:test-pool"

  # Should be able to view all managed and unmanaged networks
  host_networks="$(ip a | grep -P '^\d+:' | cut -d' ' -f2 | tr -d ':' | grep -vP '^veth.*' | sort)"
  lxd_networks="$(lxc_remote query "${remote}:/1.0/networks?recursion=1" | jq -r '.[].name' | sort)"
  [ "${host_networks}" = "${lxd_networks}" ]
}

user_is_server_operator() {
  remote="${1}"

  # Should be able to see projects.
  lxc_remote project list "${remote}:" -f csv | grep -Fq 'default'

  # Should be able to create/edit/delete a project.
  lxc_remote project create "${remote}:test-project"
  lxc_remote project show "${remote}:test-project" | sed -e 's/description: ""/description: "Test Project"/' | lxc_remote project edit "${remote}:test-project"
  lxc_remote project delete "${remote}:test-project"
}

user_can_edit_projects() {
  remote="${1}"

  lxc_remote project set "${remote}:default" user.foo bar
  lxc_remote project unset "${remote}:default" user.foo
}

user_is_not_project_manager() {
  remote="${1}"

  ! lxc_remote project set "${remote}:default" user.foo bar || false
  ! lxc_remote project unset "${remote}:default" user.foo || false
}

user_is_project_operator() {
  remote="${1}"

    # Should be able to create/edit/delete project level resources
    lxc_remote profile create "${remote}:test-profile"
    lxc_remote profile device add "${remote}:test-profile" eth0 none
    lxc_remote profile delete "${remote}:test-profile"
    lxc_remote network create "${remote}:test-network"
    lxc_remote network set "${remote}:test-network" bridge.mtu=1500
    lxc_remote network delete "${remote}:test-network"
    lxc_remote network acl create "${remote}:test-network-acl"
    lxc_remote network acl delete "${remote}:test-network-acl"
    lxc_remote network zone create "${remote}:test-network-zone"
    lxc_remote network zone delete "${remote}:test-network-zone"
    pool_name="$(lxc_remote storage list "${remote}:" -f csv | cut -d, -f1)"
    lxc_remote storage volume create "${remote}:${pool_name}" test-volume
    lxc_remote query "${remote}:/1.0/storage-volumes" | grep -F "/1.0/storage-pools/${pool_name}/volumes/custom/test-volume"
    lxc_remote query "${remote}:/1.0/storage-volumes/custom" | grep -F "/1.0/storage-pools/${pool_name}/volumes/custom/test-volume"
    lxc_remote storage volume delete "${remote}:${pool_name}" test-volume
    lxc_remote launch testimage "${remote}:operator-foo"
    LXC_LOCAL='' lxc_remote exec "${remote}:operator-foo" -- echo "bar"
    lxc_remote delete "${remote}:operator-foo" --force
}

user_is_not_project_operator() {
  remote="${1}"

  # Project list will not fail but there will be no output.
  [ "$(lxc project list "${remote}:" -f csv | wc -l)" = 0 ]
  ! lxc project show "${remote}:default" || false

  # Should not be able to see or create any instances.
  lxc_remote init testimage c1
  [ "$(lxc_remote list "${remote}:" -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote list "${remote}:" -f csv --all-projects | wc -l)" = 0 ]
  ! lxc_remote init testimage "${remote}:test-instance" || false
  lxc_remote delete c1 -f

  # Should not be able to see network allocations.
  [ "$(lxc_remote network list-allocations "${remote}:" -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote network list-allocations "${remote}:" --all-projects -f csv | wc -l)" = 0 ]

  # Should not be able to see or create networks.
  [ "$(lxc_remote network list "${remote}:" -f csv | wc -l)" = 0 ]
  ! lxc_remote network create "${remote}:test-network" || false

  # Should not be able to see or create network ACLs.
  lxc_remote network acl create acl1
  [ "$(lxc_remote network acl list "${remote}:" -f csv | wc -l)" = 0 ]
  ! lxc_remote network acl create "${remote}:test-acl" || false
  lxc_remote network acl delete acl1

  # Should not be able to see or create network zones.
  lxc_remote network zone create zone1
  [ "$(lxc_remote network zone list "${remote}:" -f csv | wc -l)" = 0 ]
  ! lxc_remote network zone create "${remote}:test-zone" || false
  lxc_remote network zone delete zone1

  # Should not be able to see or create profiles.
  [ "$(lxc_remote profile list "${remote}:" -f csv | wc -l)" = 0 ]
  ! lxc_remote profile create "${remote}:test-profile" || false

  # Should not be able to see or create image aliases
  test_image_fingerprint="$(lxc_remote image info testimage | awk '/^Fingerprint/ {print $2}')"
  [ "$(lxc_remote image alias list "${remote}:" -f csv | wc -l)" = 0 ]
  ! lxc_remote image alias create "${remote}:testimage2" "${test_image_fingerprint}" || false

  # Should not be able to see or create storage pool volumes.
  pool_name="$(lxc_remote storage list "${remote}:" -f csv | cut -d, -f1)"
  lxc_remote storage volume create "${pool_name}" vol1
  [ "$(lxc_remote storage volume list "${remote}:${pool_name}" -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote storage volume list "${remote}:${pool_name}" --all-projects -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote storage volume list "${remote}:" -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote storage volume list "${remote}:" --all-projects -f csv | wc -l)" = 0 ]
  ! lxc_remote storage volume create "${remote}:${pool_name}" test-volume || false
  lxc_remote storage volume delete "${pool_name}" vol1

  # Should not be able to see any operations.
  [ "$(lxc_remote operation list "${remote}:" -f csv | wc -l)" = 0 ]
  [ "$(lxc_remote operation list "${remote}:" --all-projects -f csv | wc -l)" = 0 ]

  # Image list will still work but none will be shown because none are public.
  [ "$(lxc_remote image list "${remote}:" -f csv | wc -l)" = 0 ]

  # Image edit will fail. Note that this fails with "not found" because we fail to resolve the alias (image is not public
  # so it is not returned from the DB).
  ! lxc_remote image set-property "${remote}:testimage" requirements.secureboot true || false
  test_image_fingerprint_short="$(echo "${test_image_fingerprint}" | cut -c1-12)"
  ! lxc_remote image set-property "${remote}:${test_image_fingerprint_short}" requirements.secureboot true || false

  # Should be able to list public images.
  lxc_remote image show testimage | sed -e "s/public: false/public: true/" | lxc_remote image edit testimage
  lxc_remote image list "${remote}:" -f csv | grep -Fq "${test_image_fingerprint_short}"
  lxc_remote image show testimage | sed -e "s/public: true/public: false/" | lxc_remote image edit testimage
}

user_is_instance_user() {
  remote="${1}"
  instance_name="${2}"

  # Check we can still interact with the instance.
  touch "${TEST_DIR}/tmp"
  lxc_remote file push "${TEST_DIR}/tmp" "${remote}:${instance_name}/root/tmpfile.txt"
  LXC_LOCAL='' lxc_remote exec "${remote}:${instance_name}" -- rm /root/tmpfile.txt
  rm "${TEST_DIR}/tmp"

  # We can't edit the instance though
  ! lxc_remote config set "${remote}:${instance_name}" user.fizz=buzz || false
}

auth_project_features() {
  ensure_import_testimage
  remote="${1}"

  # test-group must have no permissions to start the test.
  [ "$(lxc query /1.0/auth/groups/test-group | jq '.permissions | length')" -eq 0 ]

  # Create project blah
  lxc project create blah

  # Validate view with no permissions
  [ "$(lxc_remote project list "${remote}:" --format csv | wc -l)" -eq 0 ]

  # Allow operator permissions on project blah
  lxc auth group permission add test-group project blah operator

  # Confirm we can still view storage pools
  [ "$(lxc_remote storage list "${remote}:" --format csv | wc -l)" = 1 ]

  # Confirm we cannot view storage pool configuration
  pool_name="$(lxc_remote storage list "${remote}:" --format csv | cut -d, -f1)"
  [ "$(lxc_remote storage get "${remote}:${pool_name}" source)" = "" ]

  # Validate restricted view
  ! lxc_remote project list "${remote}:" --format csv | grep -w ^default || false
  lxc_remote project list "${remote}:" --format csv | grep -w ^blah

  # Validate that the restricted caller cannot edit or delete the project.
  ! lxc_remote project set "${remote}:blah" user.foo=bar || false
  ! lxc_remote project delete "${remote}:blah" || false

  # Validate restricted caller cannot create projects.
  ! lxc_remote project create "${remote}:blah1" || false

  # Validate restricted caller cannot see resources in projects they do not have access to (the call will not fail, but
  # the lists should be empty
  [ "$(lxc_remote list "${remote}:" --project default --format csv)" = "" ]
  [ "$(lxc_remote profile list "${remote}:" --project default --format csv)" = "" ]
  [ "$(lxc_remote network list "${remote}:" --project default --format csv)" = "" ]
  [ "$(lxc_remote operation list "${remote}:" --project default --format csv)" = "" ]
  [ "$(lxc_remote network zone list "${remote}:" --project default --format csv)" = "" ]
  [ "$(lxc_remote storage volume list "${remote}:${pool_name}" --project default --format csv)" = "" ]
  [ "$(lxc_remote storage bucket list "${remote}:${pool_name}" --project default --format csv)" = "" ]

  ### Validate images.
  test_image_fingerprint="$(lxc image info testimage --project default | awk '/^Fingerprint/ {print $2}')"

  # We can always list images, but there are no public images in the default project now, so the list should be empty.
  [ "$(lxc_remote image list "${remote}:" --project default --format csv)" = "" ]
  ! lxc_remote image show "${remote}:testimage" --project default || false

  # Set the image to public and ensure we can view it.
  lxc image show testimage --project default | sed -e "s/public: false/public: true/" | lxc image edit testimage --project default
  [ "$(lxc_remote image list "${remote}:" --project default --format csv | wc -l)" = 1 ]
  lxc_remote image show "${remote}:testimage" --project default

  # Check we can export the public image:
  lxc image export "${remote}:testimage" "${TEST_DIR}/" --project default
  [ "${test_image_fingerprint}" = "$(sha256sum "${TEST_DIR}/${test_image_fingerprint}.tar.xz" | cut -d' ' -f1)" ]

  # While the image is public, copy it to the blah project and create an alias for it.
  lxc_remote image copy "${remote}:testimage" "${remote}:" --project default --target-project blah
  lxc_remote image alias create "${remote}:testimage" "${test_image_fingerprint}" --project blah

  # Restore privacy on the test image in the default project.
  lxc image show testimage --project default | sed -e "s/public: true/public: false/" | lxc image edit testimage --project default

  # Set up a profile in the blah project. Additionally ensures project operator can edit profiles.
  lxc profile show default | lxc_remote profile edit "${remote}:default" --project blah

  # Create an instance (using the test image copied from the default project while it was public).
  lxc_remote init testimage "${remote}:blah-instance" --project blah

  # Create a custom volume.
  lxc_remote storage volume create "${remote}:${pool_name}" blah-volume --project blah

  # There should now be two volume URLs, one instance, one image, and one profile URL in the used-by list.
  [ "$(lxc_remote project list "${remote}:" --format csv | cut -d, -f9)" = "5" ]

  # Delete resources in project blah so that we can modify project features.
  lxc_remote delete "${remote}:blah-instance" --project blah
  lxc_remote storage volume delete "${remote}:${pool_name}" blah-volume --project blah
  lxc_remote image delete "${remote}:${test_image_fingerprint}" --project blah

  # Ensure we can create and view resources that are not enabled for the project (e.g. their effective project is
  # the default project).

  ### IMAGES (initial value is true for new projects)

  # Unset the images feature (the default is false).
  lxc project unset blah features.images

  # The test image in the default project *not* should be visible by default via project blah.
  ! lxc_remote image info "${remote}:${test_image_fingerprint}" --project blah || false
  ! lxc_remote image show "${remote}:${test_image_fingerprint}" --project blah || false
  test_image_fingerprint_short="$(echo "${test_image_fingerprint}" | cut -c1-12)"
  ! lxc_remote image list "${remote}:" --project blah | grep -F "${test_image_fingerprint_short}" || false

  # Make the images in the default project viewable to members of test-group
  lxc auth group permission add test-group project default can_view_images

  # The test image in the default project should now be visible via project blah.
  lxc_remote image info "${remote}:${test_image_fingerprint}" --project blah
  lxc_remote image show "${remote}:${test_image_fingerprint}" --project blah
  lxc_remote image list "${remote}:" --project blah | grep -F "${test_image_fingerprint_short}"

  # Members of test-group can view it via project default. (This is true even though they do not have can_view on project default).
  lxc_remote image info "${remote}:${test_image_fingerprint}" --project default
  lxc_remote image show "${remote}:${test_image_fingerprint}" --project default
  lxc_remote image list "${remote}:" --project default | grep -F "${test_image_fingerprint_short}"

  # Members of test-group cannot edit the image.
  ! lxc_remote image set-property "${remote}:${test_image_fingerprint}" requirements.secureboot true --project blah || false
  ! lxc_remote image unset-property "${remote}:${test_image_fingerprint}" requirements.secureboot --project blah || false

  # Members of test-group cannot delete the image.
  ! lxc_remote image delete "${remote}:${test_image_fingerprint}" --project blah || false

  # Delete it anyway to test that we can import a new one.
  lxc image delete "${test_image_fingerprint}" --project default

  # Members of test-group can create images.
  lxc_remote image import "${TEST_DIR}/${test_image_fingerprint}.tar.xz" "${remote}:" --project blah
  lxc_remote image alias create "${remote}:testimage" "${test_image_fingerprint}" --project blah

  # We can view the image we've created via project blah (whose effective project is default) because we've granted the
  # group permission to view all images in the default project.
  lxc_remote image show "${remote}:${test_image_fingerprint}" --project blah
  lxc_remote image show "${remote}:${test_image_fingerprint}" --project default

  # Image clean up
  lxc image delete "${test_image_fingerprint}" --project default
  lxc auth group permission remove test-group project default can_view_images
  rm "${TEST_DIR}/${test_image_fingerprint}.tar.xz"

  ### NETWORKS (initial value is false in new projects).

  # Create a network in the default project.
  networkName="net$$"
  lxc network create "${networkName}" --project default

  # The network we created in the default project is not visible in project blah.
  ! lxc_remote network show "${remote}:${networkName}" --project blah || false
  ! lxc_remote network list "${remote}:" --project blah | grep -F "${networkName}" || false

  # Make networks in the default project viewable to members of test-group
  lxc auth group permission add test-group project default can_view_networks

  # The network we created in the default project is now visible in project blah.
  lxc_remote network show "${remote}:${networkName}" --project blah
  lxc_remote network list "${remote}:" --project blah | grep -F "${networkName}"

  # Members of test-group can view it via project default.
  lxc_remote network show "${remote}:${networkName}" --project default
  lxc_remote network list "${remote}:" --project default | grep -F "${networkName}"

  # Members of test-group cannot edit the network.
  ! lxc_remote network set "${remote}:${networkName}" user.foo=bar --project blah || false

  # Members of test-group cannot delete the network.
  ! lxc_remote network delete "${remote}:${networkName}" --project blah || false

  # Create a network in the blah project.
  lxc_remote network create "${remote}:blah-network" --project blah

  # The network is visible only because we have granted view access on networks in the default project.
  lxc_remote network show "${remote}:blah-network" --project blah
  lxc_remote network list "${remote}:" --project blah | grep blah-network

  # Members of test-group can view it via the default project.
  lxc_remote network show "${remote}:blah-network" --project default

  # Members of test-group cannot edit the network.
  ! lxc_remote network set "${remote}:blah-network" user.foo=bar --project blah || false

  # Members of test-group cannot delete the network.
  ! lxc_remote network delete "${remote}:blah-network" --project blah || false

  # Network clean up
  lxc network delete "${networkName}" --project blah
  lxc network delete blah-network --project blah
  lxc auth group permission remove test-group project default can_view_networks

  ### NETWORK ZONES (initial value is false in new projects).

  # Create a network zone in the default project.
  zoneName="zone$$"
  lxc network zone create "${zoneName}" --project default

  # The network zone we created in the default project is *not* visible in project blah.
  ! lxc_remote network zone show "${remote}:${zoneName}" --project blah || false
  ! lxc_remote network zone list "${remote}:" --project blah | grep -F "${zoneName}" || false

  # Allow view access to network zones in the default project.
  lxc auth group permission add test-group project default can_view_network_zones

  # Members of test-group can now view the network zone via the default project and via the blah project.
  lxc_remote network zone show "${remote}:${zoneName}" --project default
  lxc_remote network zone list "${remote}:" --project default | grep -F "${zoneName}"
  lxc_remote network zone show "${remote}:${zoneName}" --project blah
  lxc_remote network zone list "${remote}:" --project blah | grep -F "${zoneName}"

  # Members of test-group cannot edit the network zone.
  ! lxc_remote network zone set "${remote}:${zoneName}" user.foo=bar --project blah || false

  # Members of test-group can delete the network zone.
  ! lxc_remote network zone delete "${remote}:${zoneName}" --project blah || false

  # Create a network zone in the blah project.
  lxc_remote network zone create "${remote}:blah-zone" --project blah

  # Network zone is visible to members of test-group in project blah (because they can view network zones in the default project).
  lxc_remote network zone show "${remote}:blah-zone" --project blah
  lxc_remote network zone list "${remote}:" --project blah | grep blah-zone
  lxc_remote network zone show "${remote}:blah-zone" --project default
  lxc_remote network zone list "${remote}:" --project default | grep blah-zone

  # Members of test-group cannot delete the network zone.
  ! lxc_remote network zone delete "${remote}:blah-zone" --project blah || false

  # Network zone clean up
  lxc network zone delete "${zoneName}" --project blah
  lxc network zone delete blah-zone --project blah
  lxc auth group permission remove test-group project default can_view_network_zones

  ### PROFILES (initial value is true for new projects)

  # Unset the profiles feature (the default is false).
  lxc project unset blah features.profiles

  # Create a profile in the default project.
  profileName="prof$$"
  lxc profile create "${profileName}" --project default

  # The profile we created in the default project is not visible in project blah.
  ! lxc_remote profile show "${remote}:${profileName}" --project blah || false
  ! lxc_remote profile list "${remote}:" --project blah | grep -F "${profileName}" || false

  # Grant members of test-group permission to view profiles in the default project
  lxc auth group permission add test-group project default can_view_profiles

  # The profile we just created is now visible via the default project and via the blah project
  lxc_remote profile show "${remote}:${profileName}" --project default
  lxc_remote profile list "${remote}:" --project default | grep -F "${profileName}"
  lxc_remote profile show "${remote}:${profileName}" --project blah
  lxc_remote profile list "${remote}:" --project blah | grep -F "${profileName}"

  # Members of test-group cannot edit the profile.
  ! lxc_remote profile set "${remote}:${profileName}" user.foo=bar --project blah || false

  # Members of test-group cannot delete the profile.
  ! lxc_remote profile delete "${remote}:${profileName}" --project blah || false

  # Create a profile in the blah project.
  lxc_remote profile create "${remote}:blah-profile" --project blah

  # Profile is visible to members of test-group in project blah and project default.
  lxc_remote profile show "${remote}:blah-profile" --project blah
  lxc_remote profile list "${remote}:" --project blah | grep blah-profile
  lxc_remote profile show "${remote}:blah-profile" --project default
  lxc_remote profile list "${remote}:" --project default | grep blah-profile

  # Members of test-group cannot delete the profile.
  ! lxc_remote profile delete "${remote}:blah-profile" --project blah || false

  # Profile clean up
  lxc profile delete "${profileName}" --project blah
  lxc profile delete blah-profile --project blah
  lxc auth group permission remove test-group project default can_view_profiles

  ### STORAGE VOLUMES (initial value is true for new projects)

  # Unset the storage volumes feature (the default is false).
  lxc project unset blah features.storage.volumes

  # Create a storage volume in the default project.
  volName="vol$$"
  lxc storage volume create "${pool_name}" "${volName}" --project default

  # The storage volume we created in the default project is not visible in project blah.
  ! lxc_remote storage volume show "${remote}:${pool_name}" "${volName}" --project blah || false
  ! lxc_remote storage volume list "${remote}:${pool_name}" --project blah | grep -F "${volName}" || false

  # Grant members of test-group permission to view storage volumes in project default
  lxc auth group permission add test-group project default can_view_storage_volumes

  # Members of test-group can't view it via project default and project blah.
  lxc_remote storage volume show "${remote}:${pool_name}" "${volName}" --project default
  lxc_remote storage volume list "${remote}:${pool_name}" --project default | grep -F "${volName}"
  lxc_remote storage volume show "${remote}:${pool_name}" "${volName}" --project blah
  lxc_remote storage volume list "${remote}:${pool_name}" --project blah | grep -F "${volName}"

  # Members of test-group cannot edit the storage volume.
  ! lxc_remote storage volume set "${remote}:${pool_name}" "${volName}" user.foo=bar --project blah || false

  # Members of test-group cannot delete the storage volume.
  ! lxc_remote storage volume delete "${remote}:${pool_name}" "${volName}" --project blah || false

  # Create a storage volume in the blah project.
  lxc_remote storage volume create "${remote}:${pool_name}" blah-volume --project blah

  # Storage volume is visible to members of test-group in project blah (because they can view volumes in the default project).
  lxc_remote storage volume show "${remote}:${pool_name}" blah-volume --project blah
  lxc_remote storage volume list "${remote}:${pool_name}" --project blah | grep blah-volume
  lxc_remote storage volume show "${remote}:${pool_name}" blah-volume --project default
  lxc_remote storage volume list "${remote}:${pool_name}" --project default | grep blah-volume

  # Members of test-group cannot delete the storage volume.
  ! lxc_remote storage volume delete "${remote}:${pool_name}" blah-volume --project blah || false

  # Storage volume clean up
  lxc storage volume delete "${pool_name}" "${volName}"
  lxc storage volume delete "${pool_name}" blah-volume
  lxc auth group permission remove test-group project default can_view_storage_volumes

  ### STORAGE BUCKETS (initial value is true for new projects)

  # Create a storage pool to use with object storage.
  create_object_storage_pool s3

  # Unset the storage buckets feature (the default is false).
  lxc project unset blah features.storage.buckets

  # Create a storage bucket in the default project.
  bucketName="bucket$$"
  lxc storage bucket create s3 "${bucketName}" --project default

  # The storage bucket we created in the default project is not visible in project blah.
  ! lxc_remote storage bucket show "${remote}:s3" "${bucketName}" --project blah || false
  ! lxc_remote storage bucket list "${remote}:s3" --project blah | grep -F "${bucketName}" || false

  # Grant view permission on storage buckets in project default to members of test-group
  lxc auth group permission add test-group project default can_view_storage_buckets

  # Members of test-group can now view the bucket via project default and project blah.
  lxc_remote storage bucket show "${remote}:s3" "${bucketName}" --project default
  lxc_remote storage bucket list "${remote}:s3" --project default | grep -F "${bucketName}"
  lxc_remote storage bucket show "${remote}:s3" "${bucketName}" --project blah
  lxc_remote storage bucket list "${remote}:s3" --project blah | grep -F "${bucketName}"

  # Members of test-group cannot edit the storage bucket.
  ! lxc_remote storage bucket set "${remote}:s3" "${bucketName}" user.foo=bar --project blah || false

  # Members of test-group cannot delete the storage bucket.
  ! lxc_remote storage bucket delete "${remote}:s3" "${bucketName}" --project blah || false

  # Create a storage bucket in the blah project.
  lxc_remote storage bucket create "${remote}:s3" blah-bucket --project blah

  # Storage bucket is visible to members of test-group in project blah (because they can view buckets in the default project).
  lxc_remote storage bucket show "${remote}:s3" blah-bucket --project blah
  lxc_remote storage bucket list "${remote}:s3" --project blah | grep blah-bucket

  # Members of test-group cannot delete the storage bucket.
  ! lxc_remote storage bucket delete "${remote}:s3" blah-bucket --project blah || false

  # Cleanup storage buckets
  lxc storage bucket delete s3 blah-bucket --project blah
  lxc storage bucket delete s3 "${bucketName}" --project blah
  lxc auth group permission remove test-group project default can_view_storage_buckets
  delete_object_storage_pool s3

  # General clean up
  lxc project delete blah
}