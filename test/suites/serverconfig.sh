test_server_config() {
  LXD_SERVERCONFIG_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_SERVERCONFIG_DIR}" true
  ensure_has_localhost_remote "${LXD_ADDR}"

  _server_config_access
  _server_config_storage

  kill_lxd "${LXD_SERVERCONFIG_DIR}"
}

_server_config_access() {
  # test untrusted server GET
  my_curl -X GET "https://$(cat "${LXD_SERVERCONFIG_DIR}/lxd.addr")/1.0" | grep -v -q environment

  # test authentication type
  curl --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0" | jq .metadata.auth_methods | grep tls

  # only tls is enabled by default
  ! curl --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0" | jq .metadata.auth_methods | grep oidc || false
}

_server_config_storage() {
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" = "ceph" ]; then
    return
  fi

  ensure_import_testimage
  pool=$(lxc profile device get default root pool)

  lxc init testimage foo
  lxc query --wait /1.0/containers/foo/backups -X POST -d '{\"expires_at\": \"2100-01-01T10:00:00-05:00\"}'

  # Record before
  BACKUPS_BEFORE=$(find "${LXD_DIR}/backups/" | sort)
  IMAGES_BEFORE=$(find "${LXD_DIR}/images/" | sort)

  lxc storage volume create "${pool}" backups
  lxc storage volume create "${pool}" images

  # Validate errors
  ! lxc config set storage.backups_volume foo/bar
  ! lxc config set storage.images_volume foo/bar
  ! lxc config set storage.backups_volume "${pool}/bar"
  ! lxc config set storage.images_volume "${pool}/bar"

  lxc storage volume snapshot "${pool}" backups
  lxc storage volume snapshot "${pool}" images
  ! lxc config set storage.backups_volume "${pool}/backups"
  ! lxc config set storage.images_volume "${pool}/images"

  lxc storage volume delete "${pool}" backups/snap0
  lxc storage volume delete "${pool}" images/snap0

  # Set the configuration
  lxc config set storage.backups_volume "${pool}/backups"
  lxc config set storage.images_volume "${pool}/images"

  # Record after
  BACKUPS_AFTER=$(find "${LXD_DIR}/backups/" | sort)
  IMAGES_AFTER=$(find "${LXD_DIR}/images/" | sort)

  # Validate content
  if [ "${BACKUPS_BEFORE}" != "${BACKUPS_AFTER}" ]; then
    echo "Backups dir content mismatch"
    false
  fi

  if [ "${IMAGES_BEFORE}" != "${IMAGES_AFTER}" ]; then
    echo "Images dir content mismatch"
    false
  fi

  # Validate more errors
  ! lxc storage volume delete "${pool}" backups
  ! lxc storage volume delete "${pool}" images
  ! lxc storage volume rename "${pool}" backups backups1
  ! lxc storage volume rename "${pool}" images images1
  ! lxc storage volume snapshot "${pool}" backups
  ! lxc storage volume snapshot "${pool}" images

  # Modify container and publish to image on custom volume.
  lxc start foo
  lxc exec foo -- touch /root/foo
  lxc stop -f foo
  lxc publish foo --alias fooimage

  # Launch container from published image on custom volume.
  lxc init fooimage foo2
  lxc delete -f foo2
  lxc image delete fooimage

  # Reset and cleanup
  lxc config unset storage.backups_volume
  lxc config unset storage.images_volume
  lxc storage volume delete "${pool}" backups
  lxc storage volume delete "${pool}" images
  lxc delete -f foo
}
