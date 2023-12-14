test_rbac() {
  lxc config set core.https_address "${LXD_ADDR}"
  ensure_has_localhost_remote "${LXD_ADDR}"
  ensure_import_testimage

  # Configure RBAC authentication and authorization.
  rbac_address="$(cat "${TEST_DIR}/rbac.addr")"
  rbac_url="http://${rbac_address}"
  key=$(curl -s "${rbac_url}/auth/discharge/info" | jq .PublicKey)

  # We only need to set the API key and URL.
  lxc config set rbac.api.key "${key}"
  lxc config set rbac.api.url "${rbac_url}"

  # The following config keys must be set for the driver to be loaded as they configure TLS for authentication. However
  # in this case the test RBAC server is not using TLS, so we can set them to anything.
  lxc config set rbac.agent.url "https://nonsense.com"
  lxc config set rbac.agent.private_key nonsensenonsensenonsensenonsensenonsensenon
  lxc config set rbac.agent.public_key nonsensenonsensenonsensenonsensenonsensenon
  lxc config set rbac.agent.username nonsense

  (
  cat <<EOF
user1
pass1
EOF
  ) | lxc remote add rbac "https://$LXD_ADDR" --auth-type candid --accept-certificate

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"": ["admin"]}'
  lxc list rbac:
  lxc list rbac: --all-projects
  lxc launch testimage rbac:c1
  lxc network create rbac:n1 --type bridge
  lxc storage create rbac:s1 dir
  lxc project create rbac:p1
  lxc project delete rbac:p1
  lxc profile create empty
  lxc storage volume create rbac:s1 v1

  testimage_fingerprint="$(lxc image ls -f csv | grep -F 'testimage' | cut -d, -f2)"

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["view"]}'
  lxc project show rbac:default > /dev/null
  lxc config show rbac:c1 > /dev/null
  lxc image show "rbac:${testimage_fingerprint}" > /dev/null
  lxc network show rbac:n1 > /dev/null
  lxc profile show rbac:default > /dev/null
  lxc storage volume show rbac:s1 v1 > /dev/null
  ! lxc project set rbac:default user.foo=bar || false # Does not have  "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  ! lxc delete rbac:c1 || false # Does not have "manage-containers"
  ! LXC_LOCAL='' lxc_remote exec rbac:c1 -- true || false # Does not have "operate-containers"
  ! lxc image delete "rbac:${testimage_fingerprint}" || false # Does not have "manage-images"
  ! lxc network delete rbac:n1 || false # Does not have "manage-networks"
  ! lxc profile delete rbac:empty || false # Does not have "manage-profiles"
  ! lxc storage volume delete rbac:s1 v1 || false # Does not have "manage-storage-volumes"

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["manage-projects", "view"]}'
  lxc project set rbac:default user.foo=bar # Has "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  ! lxc delete rbac:c1 || false # Does not have "manage-containers"
  ! LXC_LOCAL='' lxc_remote exec rbac:c1 -- true || false # Does not have "operate-containers"
  ! lxc image delete "rbac:${testimage_fingerprint}" || false # Does not have "manage-images"
  ! lxc network delete rbac:n1 || false # Does not have "manage-networks"
  ! lxc profile delete rbac:empty || false # Does not have "manage-profiles"
  ! lxc storage volume delete rbac:s1 v1 || false # Does not have "manage-storage-volumes"

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["manage-containers", "view"]}'
  ! lxc project set rbac:default user.foo=bar || false # Does not have "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  lxc config set rbac:c1 user.foo=bar # Has "manage-containers"
  ! LXC_LOCAL='' lxc_remote exec rbac:c1 -- true || false # Does not have "operate-containers"
  ! lxc image delete "rbac:${testimage_fingerprint}" || false # Does not have "manage-images"
  ! lxc network delete rbac:n1 || false # Does not have "manage-networks"
  ! lxc profile delete rbac:empty || false # Does not have "manage-profiles"
  ! lxc storage volume delete rbac:s1 v1 || false # Does not have "manage-storage-volumes"

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["operate-containers", "view"]}'
  ! lxc project set rbac:default user.foo=bar || false # Does not have "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  ! lxc delete rbac:c1 || false # Does not have "manage-containers"
  LXC_LOCAL='' lxc_remote exec rbac:c1 -- true # Has "operate-containers"
  ! lxc image delete "rbac:${testimage_fingerprint}" || false # Does not have "manage-images"
  ! lxc network delete rbac:n1 || false # Does not have "manage-networks"
  ! lxc profile delete rbac:empty || false # Does not have "manage-profiles"
  ! lxc storage volume delete rbac:s1 v1 || false # Does not have "manage-storage-volumes"

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["manage-images", "view"]}'
  ! lxc project set rbac:default user.foo=bar || false # Does not have "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  ! lxc delete rbac:c1 || false # Does not have "manage-containers"
  ! LXC_LOCAL='' lxc_remote exec rbac:c1 -- true || false # Does not have "operate-containers"
  lxc image delete "rbac:${testimage_fingerprint}" # Has "manage-images"
  ! lxc network delete rbac:n1 || false # Does not have "manage-networks"
  ! lxc profile delete rbac:empty || false # Does not have "manage-profiles"
  ! lxc storage volume delete rbac:s1 v1 || false # Does not have "manage-storage-volumes"

  # Re-import the test image as we just deleted it.
  ensure_import_testimage

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["manage-networks", "view"]}'
  ! lxc project set rbac:default user.foo=bar || false # Does not have "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  ! lxc delete rbac:c1 || false # Does not have "manage-containers"
  ! LXC_LOCAL='' lxc_remote exec rbac:c1 -- true || false # Does not have "operate-containers"
  ! lxc image delete "rbac:${testimage_fingerprint}" || false # Does not have "manage-images"
  lxc network delete rbac:n1 # Has "manage-networks"
  ! lxc profile delete rbac:empty || false # Does not have "manage-profiles"
  ! lxc storage volume delete rbac:s1 v1 || false # Does not have "manage-storage-volumes"

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["manage-profiles", "view"]}'
  ! lxc project set rbac:default user.foo=bar || false # Does not have "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  ! lxc delete rbac:c1 || false # Does not have "manage-containers"
  ! LXC_LOCAL='' lxc_remote exec rbac:c1 -- true || false # Does not have "operate-containers"
  ! lxc image delete "rbac:${testimage_fingerprint}" || false # Does not have "manage-images"
  ! lxc network delete rbac:lxdbr0 || false # Does not have "manage-networks"
  lxc profile delete rbac:empty # Has "manage-profiles"
  ! lxc storage volume delete rbac:s1 v1 || false # Does not have "manage-storage-volumes"

  curl "${rbac_url}/set-perms?user=user1" -X POST -d '{"default": ["manage-storage-volumes", "view"]}'
  ! lxc project set rbac:default user.foo=bar || false # Does not have "manage-projects"
  ! lxc project create rbac:p2 || false # Does not have "admin"
  ! lxc delete rbac:c1 || false # Does not have "manage-containers"
  ! LXC_LOCAL='' lxc_remote exec rbac:c1 -- true || false # Does not have "operate-containers"
  ! lxc image delete "rbac:${testimage_fingerprint}" || false # Does not have "manage-images"
  ! lxc network delete rbac:n1 || false # Does not have "manage-networks"
  ! lxc profile delete rbac:empty || false # Does not have "manage-profiles"
  lxc storage volume delete rbac:s1 v1 # Has "manage-storage-volumes"

  lxc storage delete s1
  lxc delete c1 -f

  # Unset RBAC configuration.
  lxc config unset rbac.api.key
  lxc config unset rbac.api.url
  lxc config unset rbac.agent.url
  lxc config unset rbac.agent.private_key
  lxc config unset rbac.agent.public_key
  lxc config unset rbac.agent.username
}
