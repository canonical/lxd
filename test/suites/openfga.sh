test_openfga() {
  lxc config set core.https_address "${LXD_ADDR}"
  ensure_has_localhost_remote "${LXD_ADDR}"
  ensure_import_testimage

  # Set up candid authentication. It is not possible to test OpenFGA with TLS authentication, as this
  # will revert to the TLS authorization driver.
  identity_endpoint="http://$(cat "${TEST_DIR}/rbac.addr")/auth"
  lxc config set candid.api.url "${identity_endpoint}"

  key="$(curl -s "${identity_endpoint}/discharge/info" | jq -r .PublicKey)"
  lxc config set candid.api.key "${key}"

  (
  cat <<EOF
user1
pass1
EOF
  ) | lxc remote add candid-openfga "https://${LXD_ADDR}" --auth-type candid --accept-certificate

  # Run the openfga server.
  run_openfga

  # Create store and get store ID.
  OPENFGA_STORE_ID="$(fga store create --name "test" | jq -r '.store.id')"

  # Configure OpenFGA in LXD.
  lxc config set openfga.api.url "$(fga_address)"
  lxc config set openfga.api.token "$(fga_token)"
  lxc config set openfga.store.id "${OPENFGA_STORE_ID}"

  echo "==> Checking permissions for unknown user..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
  user_is_not_project_operator

  # Give the user the `admin` entitlement on `server:lxd`.
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 admin server:lxd

  echo "==> Checking permissions for server admin..."
  user_is_server_admin
  user_is_server_operator
  user_is_project_manager
  user_is_project_operator

  # Give the user the `operator` entitlement on `server:lxd`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 admin server:lxd
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 operator server:lxd

  echo "==> Checking permissions for server operator..."
  user_is_not_server_admin
  user_is_server_operator
  user_is_project_manager
  user_is_project_operator

  # Give the user the `manager` entitlement on `project:default`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 operator server:lxd
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 manager project:default

  echo "==> Checking permissions for project manager..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_project_manager
  user_is_project_operator

  # Give the user the `operator` entitlement on `project:default`.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 manager project:default
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 operator project:default

  echo "==> Checking permissions for project operator..."
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
  user_is_project_operator

  # Create an instance for testing the "instance -> user" relation.
  lxc launch testimage user-foo

  # Change permission to "user" for instance "user-foo"
  # We need to do this after the instance is created, otherwise the instance tuple won't exist in OpenFGA.
  fga tuple delete --store-id "${OPENFGA_STORE_ID}" user:user1 operator project:default
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 user instance:default/user-foo

  echo "==> Checking permissions for instance user..."
  user_is_instance_user user-foo # Pass instance name into test as we don't have permission to create one.
  lxc delete user-foo --force # Must clean this up now as subsequent tests assume a clean project.
  user_is_not_server_admin
  user_is_not_server_operator
  user_is_not_project_manager
  user_is_not_project_operator

  # Unset config keys.
  lxc config unset candid.api.url
  lxc config unset candid.api.key
  lxc config unset openfga.api.url
  lxc config unset openfga.api.token
  lxc config unset openfga.store.id
  lxc remote remove candid-openfga

  shutdown_openfga
}

user_is_not_server_admin() {
  # Can always see server info (type-bound public access https://openfga.dev/docs/modeling/public-access).
  lxc info candid-openfga: > /dev/null

  # Cannot see any config.
  ! lxc info candid-openfga: | grep -Fq 'core.https_address' || false

  # Cannot set any config.
  ! lxc config set candid-openfga: core.proxy_https=https://example.com || false

  # Should still be able to list storage pools but not be able to see any storage pool config or delete.
  [ "$(lxc storage list candid-openfga: -f csv | wc -l)" = 1 ]
  lxc storage create test-pool dir
  ! lxc storage set candid-openfga:test-pool rsync.compression=true || false
  ! lxc storage show candid-openfga:test-pool | grep -Fq 'source:' || false
  ! lxc storage delete candid-openfga:test-pool || false
  lxc storage delete test-pool

  # Should not be able to create a storage pool.
  ! lxc storage create candid-openfga:test dir || false

  # Should still be able to list certificates.
  [ "$(lxc config trust list candid-openfga: -f csv | wc -l)" = 1 ]

  # Cannot edit certificates.
  fingerprint="$(lxc config trust list -f csv | cut -d, -f4)"
  ! lxc config trust show "${fingerprint}" | sed -e "s/restricted: false/restricted: true/" | lxc config trust edit "candid-openfga:${fingerprint}" || false
}

user_is_not_server_operator() {
  # Should not be able to create a project.
  ! lxc project create candid-openfga:new-project || false
}

user_is_server_admin() {
  # Should be able to see server config.
  lxc info candid-openfga: | grep -Fq 'core.https_address'

  # Should be able to add/remove certificates.
  gen_cert openfga-test
  test_cert_fingerprint="$(cert_fingerprint "${LXD_CONF}/openfga-test.crt")"
  certificate_add_token="$(lxc config trust add candid-openfga: --name test --quiet)"
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
  mv "${LXD_CONF}/openfga-test.crt" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/openfga-test.key" "${LXD_CONF}/client.key"
  lxc remote add test-remote "${certificate_add_token}"
  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"
  lxc config trust remove "candid-openfga:${test_cert_fingerprint}"
  lxc remote remove test-remote

  # Should be able to create/edit/delete a storage pool.
  lxc storage create candid-openfga:test-pool dir
  lxc storage set candid-openfga:test-pool rsync.compression=true
  lxc storage show candid-openfga:test-pool | grep -Fq 'rsync.compression:'
  lxc storage delete candid-openfga:test-pool
}

user_is_server_operator() {
  # Should be able to see projects.
  lxc project list candid-openfga: -f csv | grep -Fq 'default'

  # Should be able to create/edit/delete a project.
  lxc project create candid-openfga:test-project
  lxc project show candid-openfga:test-project | sed -e 's/description: ""/description: "Test Project"/' | lxc project edit candid-openfga:test-project
  lxc project delete candid-openfga:test-project
}

user_is_project_manager() {
  lxc project set candid-openfga:default user.foo bar
  lxc project unset candid-openfga:default user.foo
}

user_is_not_project_manager() {
  ! lxc project set candid-openfga:default user.foo bar || false
  ! lxc project unset candid-openfga:default user.foo || false
}

user_is_project_operator() {
    # Should be able to create/edit/delete project level resources
    lxc profile create candid-openfga:test-profile
    lxc profile device add candid-openfga:test-profile eth0 none
    lxc profile delete candid-openfga:test-profile
    lxc network create candid-openfga:test-network
    lxc network set candid-openfga:test-network bridge.mtu=1500
    lxc network delete candid-openfga:test-network
    lxc network acl create candid-openfga:test-network-acl
    lxc network acl delete candid-openfga:test-network-acl
    lxc network zone create candid-openfga:test-network-zone
    lxc network zone delete candid-openfga:test-network-zone
    pool_name="$(lxc storage list candid-openfga: -f csv | cut -d, -f1)"
    lxc storage volume create "candid-openfga:${pool_name}" test-volume
    lxc storage volume delete "candid-openfga:${pool_name}" test-volume
    lxc launch testimage candid-openfga:operator-foo
    LXC_LOCAL='' lxc_remote exec candid-openfga:operator-foo -- echo "bar"
    lxc delete candid-openfga:operator-foo --force
}

user_is_not_project_operator() {

  # Project list will not fail but there will be no output.
  [ "$(lxc project list candid-openfga: -f csv | wc -l)" = 0 ]
  ! lxc project show candid-openfga:default || false

  # Should not be able to see or create any instances.
  lxc init testimage c1
  [ "$(lxc list candid-openfga: -f csv | wc -l)" = 0 ]
  [ "$(lxc list candid-openfga: -f csv --all-projects | wc -l)" = 0 ]
  ! lxc init testimage candid-openfga:test-instance || false
  lxc delete c1 -f

  # Should not be able to see network allocations.
  [ "$(lxc network list-allocations candid-openfga: -f csv | wc -l)" = 0 ]
  [ "$(lxc network list-allocations candid-openfga: --all-projects -f csv | wc -l)" = 0 ]

  # Should not be able to see or create networks.
  [ "$(lxc network list candid-openfga: -f csv | wc -l)" = 0 ]
  ! lxc network create candid-openfga:test-network || false

  # Should not be able to see or create network ACLs.
  lxc network acl create acl1
  [ "$(lxc network acl list candid-openfga: -f csv | wc -l)" = 0 ]
  ! lxc network acl create candid-openfga:test-acl || false
  lxc network acl delete acl1

  # Should not be able to see or create network zones.
  lxc network zone create zone1
  [ "$(lxc network zone list candid-openfga: -f csv | wc -l)" = 0 ]
  ! lxc network zone create candid-openfga:test-zone || false
  lxc network zone delete zone1

  # Should not be able to see or create profiles.
  [ "$(lxc profile list candid-openfga: -f csv | wc -l)" = 0 ]
  ! lxc profile create candid-openfga:test-profile || false

  # Should not be able to see or create image aliases
  test_image_fingerprint="$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
  [ "$(lxc image alias list candid-openfga: -f csv | wc -l)" = 0 ]
  ! lxc image alias create candid-openfga:testimage2 "${test_image_fingerprint}" || false

  # Should not be able to see or create storage pool volumes.
  pool_name="$(lxc storage list candid-openfga: -f csv | cut -d, -f1)"
  lxc storage volume create "${pool_name}" vol1
  [ "$(lxc storage volume list "candid-openfga:${pool_name}" -f csv | wc -l)" = 0 ]
  [ "$(lxc storage volume list "candid-openfga:${pool_name}" --all-projects -f csv | wc -l)" = 0 ]
  ! lxc storage volume create "candid-openfga:${pool_name}" test-volume || false
  lxc storage volume delete "${pool_name}" vol1

  # Should not be able to see any operations.
  [ "$(lxc operation list candid-openfga: -f csv | wc -l)" = 0 ]
  [ "$(lxc operation list candid-openfga: --all-projects -f csv | wc -l)" = 0 ]

  # Image list will still work but none will be shown because none are public.
  [ "$(lxc image list candid-openfga: -f csv | wc -l)" = 0 ]

  # Image edit will fail. Note that this fails with "not found" because we fail to resolve the alias (image is not public
  # so it is not returned from the DB).
  ! lxc image set-property candid-openfga:testimage requirements.secureboot true || false
  test_image_fingerprint_short="$(echo "${test_image_fingerprint}" | cut -c1-12)"
  ! lxc image set-property "candid-openfga:${test_image_fingerprint_short}" requirements.secureboot true || false

  # Should be able to list public images.
  lxc image show testimage | sed -e "s/public: false/public: true/" | lxc image edit testimage
  lxc image list candid-openfga: -f csv | grep -Fq "${test_image_fingerprint_short}"
  lxc image show testimage | sed -e "s/public: true/public: false/" | lxc image edit testimage
}

user_is_instance_user() {
  instance_name="${1}"

  # Check we can still interact with the instance.
  touch "${TEST_DIR}/tmp"
  lxc file push "${TEST_DIR}/tmp" "candid-openfga:${instance_name}/root/tmpfile.txt"
  LXC_LOCAL='' lxc_remote exec "candid-openfga:${instance_name}" -- rm /root/tmpfile.txt
  rm "${TEST_DIR}/tmp"

  # We can't edit the instance though
  ! lxc config set "candid-openfga:${instance_name}" user.fizz=buzz || false
}
