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
  ! my_curl -X GET "https://$(cat "${LXD_SERVERCONFIG_DIR}/lxd.addr")/1.0" | grep -wF "environment" || false

  # test authentication type, only tls is enabled by default
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0" | jq -r '.metadata.auth_methods | .[]')" = "tls" ]

  # test fetch metadata validation.
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: same-origin' "lxd/1.0")" = "200" ]
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: cross-site' "lxd/1.0")" = "403" ]
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: same-site' "lxd/1.0")" = "403" ]

  # test content type validation.
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H "Content-Type: application/json" "lxd/1.0")" = "200" ]
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H "Content-Type: foo" "lxd/1.0")" = "415" ]
}

_server_config_storage() {
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" = "ceph" ]; then
    # The volume doesn't have to be present as the check errors after testing for the remote storage pool.
    ! lxc config set storage.backups_volume "${pool}/foo" | grep -F "Error: Failed validation of \"storage.backups_volume\": Remote storage pool \"${pool}\" cannot be used" || false
    ! lxc config set storage.images_volume "${pool}/foo" | grep -F "Error: Failed validation of \"storage.images_volume\": Remote storage pool \"${pool}\" cannot be used" || false

    return
  fi

  ensure_import_testimage
  pool=$(lxc profile device get default root pool)

  lxc init testimage foo
  lxc query --wait /1.0/containers/foo/backups -X POST -d '{\"expires_at\": \"2100-01-01T10:00:00-05:00\"}'

  # Record before
  BACKUPS_BEFORE=$(cd "${LXD_DIR}/backups/" && find . | sort)
  IMAGES_BEFORE=$(cd "${LXD_DIR}/images/" && find . | sort)

  lxc storage volume create "${pool}" backups
  lxc storage volume create "${pool}" images

  # Validate errors
  ! lxc config set storage.backups_volume foo/bar || false
  ! lxc config set storage.images_volume foo/bar || false
  ! lxc config set storage.backups_volume "${pool}/bar" || false
  ! lxc config set storage.images_volume "${pool}/bar" || false

  lxc storage volume snapshot "${pool}" backups
  lxc storage volume snapshot "${pool}" images
  ! lxc config set storage.backups_volume "${pool}/backups" || false
  ! lxc config set storage.images_volume "${pool}/images" || false

  lxc storage volume delete "${pool}" backups/snap0
  lxc storage volume delete "${pool}" images/snap0

  # Set the configuration
  lxc config set storage.backups_volume "${pool}/backups"
  lxc config set storage.images_volume "${pool}/images"

  # Validate the old location is really gone
  [ ! -e "${LXD_DIR}/backups" ]
  [ ! -e "${LXD_DIR}/images" ]

  # Record after
  BACKUPS_AFTER=$(cd "${LXD_DIR}/storage-pools/${pool}/custom/default_backups/backups" && find . | sort)
  IMAGES_AFTER=$(cd "${LXD_DIR}/storage-pools/${pool}/custom/default_images/images" && find . | sort)

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
  ! lxc storage volume delete "${pool}" backups || false
  ! lxc storage volume delete "${pool}" images || false
  ! lxc storage volume rename "${pool}" backups backups1 || false
  ! lxc storage volume rename "${pool}" images images1 || false
  ! lxc storage volume snapshot "${pool}" backups || false
  ! lxc storage volume snapshot "${pool}" images || false

  # Modify container and publish to image on custom volume.
  lxc start foo
  lxc exec foo -- touch /root/foo
  lxc stop -f foo
  lxc publish foo --alias fooimage

  # Launch container from published image on custom volume.
  lxc init fooimage foo2
  lxc delete -f foo2
  lxc image delete fooimage

  # Put both storages on the same shared volume
  lxc storage volume create "${pool}" shared
  lxc config unset storage.backups_volume
  lxc config unset storage.images_volume
  lxc config set storage.backups_volume "${pool}/shared"
  lxc config set storage.images_volume "${pool}/shared"

  # Unset the config and remove the volumes
  lxc config unset storage.backups_volume
  lxc config unset storage.images_volume
  lxc storage volume delete "${pool}" backups
  lxc storage volume delete "${pool}" images
  lxc storage volume delete "${pool}" shared

  # Record again after unsetting
  BACKUPS_AFTER=$(cd "${LXD_DIR}/backups/" && find . | sort)
  IMAGES_AFTER=$(cd "${LXD_DIR}/images/" && find . | sort)

  # Validate content
  if [ "${BACKUPS_BEFORE}" != "${BACKUPS_AFTER}" ]; then
    echo "Backups dir content mismatch"
    false
  fi

  if [ "${IMAGES_BEFORE}" != "${IMAGES_AFTER}" ]; then
    echo "Images dir content mismatch"
    false
  fi

  # Cleanup
  lxc delete -f foo
}
