test_server_config() {
  LXD_SERVERCONFIG_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_SERVERCONFIG_DIR}" true
  ensure_has_localhost_remote "${LXD_ADDR}"

  _server_config_access
  _server_config_storage
  _server_config_auth_secret
  _server_config_cluster_uuid
  _server_config_user_microcloud

  kill_lxd "${LXD_SERVERCONFIG_DIR}"
}

_server_config_cluster_uuid() {
  # Validate that the cluster UUID cannot be changed

  # PUT
  ! lxc config unset volatile.uuid || false
  ! lxc config set volatile.uuid="$(uuidgen)" || false
  cluster_uuid="$(lxc config get volatile.uuid)"
  lxc config set volatile.uuid="${cluster_uuid}"

  # PATCH
  my_curl -X PATCH "https://${LXD_ADDR}/1.0" -d "{\"config\":{\"core.https_address\":\"${LXD_ADDR}\"}}" | jq -e '.status == "Success" and .status_code == 200'
  my_curl -X PATCH "https://${LXD_ADDR}/1.0" -d "{\"config\":{\"core.https_address\":\"${LXD_ADDR}\",\"volatile.uuid\":\"\"}}" | jq -e '.error == "The cluster UUID cannot be changed" and .error_code == 400'
  my_curl -X PATCH "https://${LXD_ADDR}/1.0" -d "{\"config\":{\"core.https_address\":\"${LXD_ADDR}\",\"volatile.uuid\":\"$(uuidgen)\"}}" | jq -e '.error == "The cluster UUID cannot be changed" and .error_code == 400'
  my_curl -X PATCH "https://${LXD_ADDR}/1.0" -d "{\"config\":{\"core.https_address\":\"${LXD_ADDR}\",\"volatile.uuid\":\"${cluster_uuid}\"}}" | jq -e '.status == "Success" and .status_code == 200'

  # Check that the cluster UUID matches the server UUID
  [ "$(< "${LXD_DIR}/server.uuid")" = "${cluster_uuid}" ]
}

_server_config_auth_secret() {
    # Validate core.auth_secret_expiry cannot be set to less than one day
    lxc config set core.auth_secret_expiry="1d"
    ! lxc config set core.auth_secret_expiry='23H 59M 59S' || false
    lxc config set core.auth_secret_expiry='23H 59M 60S'
    ! lxc config set core.auth_secret_expiry='1439M 59S' || false
    lxc config set core.auth_secret_expiry='1439M 60S'
    ! lxc config set core.auth_secret_expiry='86399S' || false
    lxc config set core.auth_secret_expiry='86400S'
    lxc config unset core.auth_secret_expiry
}

_server_config_access() {
  # test untrusted server GET
  ! my_curl -X GET "https://$(< "${LXD_SERVERCONFIG_DIR}/lxd.addr")/1.0" | grep -wF "environment" || false

  # test authentication type, only tls is enabled by default
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0" | jq -r '.metadata.auth_methods | .[]')" = "tls" ]

  # test fetch metadata validation.
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: same-origin' "lxd/1.0")" = "200" ]
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: cross-site' "lxd/1.0")" = "403" ]
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: same-site' "lxd/1.0")" = "403" ]

  # test content type validation.
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H "User-Agent: Mozilla/5.0" -H "Content-Type: application/json" "lxd/1.0")" = "200" ]
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H "User-Agent: Mozilla/5.0" -H "Content-Type: foo" "lxd/1.0")" = "415" ]
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H "User-Agent: LXD" -H "Content-Type: foo" "lxd/1.0")" = "200" ]

  # test that the /ui redirect works
  [ "$(curl --silent --unix-socket "$LXD_DIR/unix.socket" -w "%{url_effective}" -o /dev/null --location -H "User-Agent: Mozilla/5.0" "lxd/")" = "http://lxd/ui/" ]
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

  # Regression test for the case where project deletion unmounted the daemon storage volume.
  lxc project create foo
  lxc project delete foo
  [ -d "${LXD_DIR}/storage-pools/${pool}/custom/default_images/images" ]

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

_server_config_user_microcloud() {
  # Set config key user.microcloud, which is readable by untrusted clients
  lxc config set user.microcloud true
  [ "$(lxc config get user.microcloud)" = "true" ]
  [ "$(curl "https://${LXD_ADDR}/1.0" --insecure | jq '.metadata.config["user.microcloud"]')" = '"true"' ]

  # Set config key user.foo, which is not exposed to untrusted clients
  lxc config set user.foo bar
  [ "$(lxc config get user.foo)" = "bar" ]
  [ "$(curl "https://${LXD_ADDR}/1.0" --insecure | jq '.metadata.config["user.foo"]')" = 'null' ]

  # Unset all config and check it worked
  lxc config unset user.microcloud
  lxc config unset user.foo
  [ "$(lxc config get user.microcloud)" = "" ]
  [ "$(lxc config get user.foo)" = "" ]
  [ "$(curl "https://${LXD_ADDR}/1.0" --insecure | jq '.metadata.config["user.microcloud"]')" = 'null' ]
  [ "$(curl "https://${LXD_ADDR}/1.0" --insecure | jq '.metadata.config["user.foo"]')" = 'null' ]
}
