test_devlxd() {
  ensure_import_testimage
  fingerprint="$(lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  # Ensure testimage is not set as cached.
  lxd sql global "UPDATE images SET cached=0 WHERE fingerprint=\"${fingerprint}\""

  lxc launch testimage devlxd -c security.devlxd=false -c boot.autostart=true

  ! lxc exec devlxd -- test -S /dev/lxd/sock || false
  lxc config unset devlxd security.devlxd
  lxc exec devlxd -- test -S /dev/lxd/sock
  lxc file push --quiet "$(command -v devlxd-client)" devlxd/bin/

  ### Test bearer token authentication

  # Check that auth is untrusted by default
  lxc exec devlxd -- devlxd-client get-state | jq --exit-status '.auth == "untrusted"'

  # Create a bearer identity and issue a token
  lxc auth identity create devlxd/foo
  devlxd_token1="$(lxc auth identity token issue devlxd/foo --quiet)"

  # Check that the token is valid (devlxd can be called with the token and auth is trusted).
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token1}" devlxd -- devlxd-client get-state | jq --exit-status '.auth == "trusted"'

  # Issue another token, the old token should be invalid (so devlxd calls fail) and the new one valid.
  devlxd_token2="$(lxc auth identity token issue devlxd/foo --quiet)"
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token1}" devlxd -- devlxd-client get-state || false)" = 'Failed to verify bearer token: Failed to authenticate bearer token: Token is not valid: token signature is invalid: signature is invalid' ]
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token2}" devlxd -- devlxd-client get-state | jq --exit-status '.auth == "trusted"'

  # Revoke the token, it should no longer be valid.
  subject="$(lxc query /1.0/auth/identities/bearer/foo | jq --exit-status --raw-output .id)"
  lxc auth identity token revoke devlxd/foo
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token2}" devlxd -- devlxd-client get-state || false)" = "Failed to verify bearer token: Identity \"${subject}\" (bearer) not found" ]

  # Issue a new token, it should be valid
  devlxd_token3="$(lxc auth identity token issue devlxd/foo --quiet)"
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token3}" devlxd -- devlxd-client get-state | jq --exit-status '.auth == "trusted"'

  # Delete the identity, the token should no longer be valid.
  lxc auth identity delete devlxd/foo
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token3}" devlxd -- devlxd-client get-state || false)" = "Failed to verify bearer token: Identity \"${subject}\" (bearer) not found" ]

  # Create a token with an expiry
  lxc auth identity create devlxd/foo
  # Note: a shorter expiry sometimes causes `devlxd-client` output garbage leading to parser error in `jq`
  devlxd_token4="$(lxc auth identity token issue devlxd/foo --quiet --expiry 2S)"

  # It's initially valid
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token4}" devlxd -- devlxd-client get-state | jq --exit-status '.auth == "trusted"'

  # It's not valid after the expiry
  sleep 3
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token4}" devlxd -- devlxd-client get-state || false)" = 'Failed to verify bearer token: Failed to authenticate bearer token: Token is not valid: token has invalid claims: token is expired' ]

  # Clean up
  lxc auth identity delete devlxd/foo

  # No secret remains in the database after the identity was deleted
  [ "$(lxd sql global --format csv 'SELECT COUNT(*) FROM secrets WHERE entity_id')" = "0" ]

  # Try to get a host's private image from devlxd.
  [ "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}")" = "Forbidden" ]
  lxc config set devlxd security.devlxd.images true
  # Trying to get a private image should return a not found error so that the client can't infer the existence
  # of an image with the provided fingerprint.
  [ "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}")" = "Not Found" ]
  lxd sql global "UPDATE images SET cached=1 WHERE fingerprint=\"${fingerprint}\""
  # No output means the export succeeded.
  [ -z "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}" || echo fail)" ]

  lxc config set devlxd user.foo=bar user.xyz="bar %s bar"
  [ "$(lxc exec devlxd -- devlxd-client user.foo)" = "bar" ]
  [ "$(lxc exec devlxd -- devlxd-client user.xyz)" = "bar %s bar" ]

  # Make sure instance configuration keys are not accessible
  [ "$(lxc exec devlxd -- devlxd-client security.nesting)" = "Forbidden" ]
  lxc config set devlxd security.nesting true
  [ "$(lxc exec devlxd -- devlxd-client security.nesting)" = "Forbidden" ]

  "${_LXC}" exec devlxd -- devlxd-client monitor-websocket > "${TEST_DIR}/devlxd-websocket.log" &
  client_websocket=$!

  "${_LXC}" exec devlxd -- devlxd-client monitor-stream > "${TEST_DIR}/devlxd-stream.log" &
  client_stream=$!

  EXPECTED_MD5="$(md5sum - << EOF
{
  "type": "config",
  "timestamp": "0001-01-01T00:00:00Z",
  "metadata": {
    "key": "user.foo",
    "old_value": "bar",
    "value": "baz"
  }
}
{
  "type": "device",
  "timestamp": "0001-01-01T00:00:00Z",
  "metadata": {
    "action": "added",
    "config": {
      "path": "/mnt",
      "source": "${TEST_DIR}",
      "type": "disk"
    },
    "name": "mnt"
  }
}
{
  "type": "device",
  "timestamp": "0001-01-01T00:00:00Z",
  "metadata": {
    "action": "removed",
    "config": {
      "path": "/mnt",
      "source": "${TEST_DIR}",
      "type": "disk"
    },
    "name": "mnt"
  }
}
EOF
)"

  MATCH=0

  for _ in $(seq 10); do
    lxc config set devlxd user.foo=bar security.nesting=true

    true > "${TEST_DIR}/devlxd-websocket.log"
    true > "${TEST_DIR}/devlxd-stream.log"

    lxc config set devlxd user.foo=baz security.nesting=false
    lxc config device add devlxd mnt disk source="${TEST_DIR}" path=/mnt
    lxc config device remove devlxd mnt

    if [ "$(tr -d '\0' < "${TEST_DIR}/devlxd-websocket.log" | md5sum)" != "${EXPECTED_MD5}" ] || [ "$(tr -d '\0' < "${TEST_DIR}/devlxd-stream.log" | md5sum)" != "${EXPECTED_MD5}" ]; then
      sleep 0.5
      continue
    fi

    MATCH=1
    break
  done

  kill -9 "${client_websocket}"
  kill -9 "${client_stream}"

  lxc monitor --type=lifecycle > "${TEST_DIR}/devlxd.log" &
  monitorDevlxdPID=$!

  # Test instance Ready state
  [ "$(lxc list -f csv -c s devlxd)" = "RUNNING" ]
  lxc exec devlxd -- devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "1" ]

  [ "$(lxc list -f csv -c s devlxd)" = "READY" ]
  lxc exec devlxd -- devlxd-client ready-state false
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "false" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "1" ]

  [ "$(lxc list -f csv -c s devlxd)" = "RUNNING" ]

  kill -9 "${monitorDevlxdPID}"
  rm "${TEST_DIR}/devlxd.log"

  # Expedite LXD shutdown by forcibly killing the running instance
  lxc stop -f devlxd

  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  # volatile.last_state.ready should be unset during daemon init
  [ -z "$(lxc config get devlxd volatile.last_state.ready || echo fail)" ]

  lxc monitor --type=lifecycle > "${TEST_DIR}/devlxd.log" &
  monitorDevlxdPID=$!

  lxc exec devlxd -- devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "1" ]

  lxc stop -f devlxd
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "false" ]

  lxc start devlxd
  lxc exec devlxd -- devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "2" ]

  # Check device configs are available and that NIC hwaddr is available even if volatile.
  hwaddr=$(lxc config get devlxd volatile.eth0.hwaddr)
  lxc exec devlxd -- devlxd-client devices | jq --exit-status ".eth0.hwaddr == \"${hwaddr}\""

  lxc delete devlxd --force
  kill -9 "${monitorDevlxdPID}"
  rm "${TEST_DIR}/devlxd.log"

  [ "${MATCH}" = "1" ]
}

test_devlxd_volume_management() {
  local testName="devlxd-volume-mgmt"

  local instPrefix="${testName}"
  local pool="${testName}"
  local project="${testName}"
  local authGroup="${testName}-group"
  local authIdentity="devlxd/${testName}-identity"

  poolDriver="$(storage_backend "$LXD_DIR")"
  lxc storage create "${pool}" "${poolDriver}"

  if [ "${project}" != "default" ]; then
    lxc project create "${project}" --config features.images=false
  fi

  local instTypes="container"
  ensure_import_testimage

  if [ "${LXD_VM_TESTS}" != "0" ]; then
    ensure_import_ubuntu_vm_image
    instTypes="${instTypes} virtual-machine"
  fi

  for instType in $instTypes; do
    inst="${instPrefix}-${instType}"

    image="testimage"
    opts="--storage ${pool}"
    if [ "${instType}" = "virtual-machine" ]; then
        image="ubuntu-vm"
        opts="$opts --vm --config limits.memory=384MiB --device ${SMALL_VM_ROOT_DISK}"
    fi

    # shellcheck disable=SC2086 # Variable "opts" must not be quoted, we want word splitting.
    lxc launch "${image}" "${inst}" $opts --project "${project}"
    waitInstanceReady "${inst}" "${project}"

    # Install devlxd-client and make sure it works.
    lxc file push --project "${project}" --quiet "$(command -v devlxd-client)" "${inst}/bin/"
    lxc exec --project "${project}" "${inst}" -- devlxd-client

    # Ensure supported storage drivers are included in /1.0 only when volume management security flag is enabled.
    lxc exec "${inst}" --project "${project}" -- devlxd-client get-state | jq --exit-status '.supported_storage_drivers == []'
    lxc config set "${inst}" --project "${project}" security.devlxd.management.volumes=true
    lxc exec "${inst}" --project "${project}" -- devlxd-client get-state | jq --exit-status '.supported_storage_drivers | length > 0'
    lxc exec "${inst}" --project "${project}" -- devlxd-client get-state | jq --exit-status '.supported_storage_drivers[] | select(.name == "dir") | .remote == false'

    # Test devLXD authentication (devLXD identity).
    # Fail when token is not passed.
    [ "$(lxc exec "${inst}" --project "${project}" -- devlxd-client instance get "${inst}")" = "You must be authenticated" ]

    # Ensure "environment" is not included in the API response for unauthenticated clients.
    # When using LXD go-client, default values are used for missing fields, so "environment.server_clustered" will be false.
    lxc exec "${inst}" --project "${project}" -- devlxd-client get-state | jq --exit-status '.environment.server_clustered == false'
    # However, "environment" must be missing in the API response.
    lxc exec "${inst}" --project "${project}" -- devlxd-client query GET /1.0 | jq --exit-status '.environment == null'

    # Fail when a valid identity token is passed, but the identity does not have permissions.
    lxc auth identity create "${authIdentity}"
    token=$(lxc auth identity token issue "${authIdentity}" --quiet)
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst}")" = "Not Found" ]

    # Fail when invalid token is passed (replace signature part).
    invalidToken="${token%.*}.invalid"
    ! lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${invalidToken}" -- devlxd-client instance get "${inst}" || false

    # Succeed when a valid identity token is passed and the identity has permissions.
    lxc auth group create "${authGroup}"
    lxc auth group permission add "${authGroup}" project "${project}" can_view
    lxc auth group permission add "${authGroup}" instance "${inst}" can_view project="${project}"
    lxc auth identity group add "${authIdentity}" "${authGroup}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst}" | jq --exit-status .name
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client get-state | jq --exit-status '.environment.server_clustered == false'
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client query GET /1.0 | jq --exit-status '.environment.server_clustered == false'

    # Test devLXD authorization (volume management security flag).
    # Fail when the security flag is not set.
    lxc config set "${inst}" --project "${project}" security.devlxd.management.volumes=false
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst}")" = "Forbidden" ]
    lxc config set "${inst}" --project "${project}" security.devlxd.management.volumes=true

    # Get storage pool.
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get invalid-pool)" = "Storage pool not found" ]
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get "${pool}"

    # Get storage volumes (ok - custom volumes requested).
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage volumes "${pool}" | jq --exit-status '. == []'

    # Get storage volume (fail - insufficient permissions).
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" "${instType}" "${inst}")" = "Not Found" ]

    # Grant storage volume view permission.
    lxc auth group permission add "${authGroup}" project "${project}" can_view_storage_volumes

    # Get storage volume (fail - non-custom volume requested).
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" "${instType}" "${inst}")" = "Only custom storage volume requests are allowed" ]

    # Create a custom storage volume.
    vol1='{"name": "vol-01", "type": "custom", "config": {"size": "8MiB"}}'

    # Create a custom storage volume (fail - insufficient permissions).
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${vol1}")" = "Forbidden" ]

    # Grant storage volume create permission.
    lxc auth group permission add "${authGroup}" project "${project}" can_create_storage_volumes

    # Create a custom storage volumes (ok).
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${vol1}"

    vol2='{"name": "vol-02", "type": "custom", "config": {"size": "8MiB"}}'
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${vol2}"

    # Fail - already exists.
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${vol2}")" = "Volume by that name already exists" ]

    # Verify created storage volumes.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage volumes "${pool}" | jq --exit-status 'length == 2'
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" custom vol-01
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" custom vol-02

    # Update storage volume.
    volNew='{"description": "Updated volume", "config": {"size": "12MiB"}}'

    # Update storage volume (fail - insufficient permissions).
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage update-volume "${pool}" custom vol-01 "${volNew}")" = "Forbidden" ]

    # Grant storage volume edit permission.
    lxc auth group permission add "${authGroup}" project "${project}" can_edit_storage_volumes

    # Update storage volume (fail - non-custom volume).
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage update-volume "${pool}" "${instType}" "${inst}" "${volNew}")" = "Only custom storage volume requests are allowed" ]

    # Update storage volume (fail - incorrect ETag).
    ! lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage update-volume "${pool}" custom vol-01 "${volNew}" incorrect-etag || false

    # Update storage volume (ok - no ETag).
    etag=$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume-etag "${pool}" custom vol-01)
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage update-volume "${pool}" custom vol-01 "${volNew}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" custom vol-01 | jq --exit-status '.config.size == "12MiB" and .description == "Updated volume"'

    # Update storage volume (ok - correct ETag).
    etag=$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume-etag "${pool}" custom vol-02)
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage update-volume "${pool}" custom vol-02 "${volNew}" "${etag}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" custom vol-02 | jq --exit-status '.config.size == "12MiB" and .description == "Updated volume"'

    # Get instance.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst}"

    # Attach new device.
    attachReq=$(cat <<EOF
{
    "devices": {
        "vol-01": {
            "type": "disk",
            "pool": "${pool}",
            "source": "vol-01",
            "path": "/mnt/vol-01"
        }
    }
}
EOF
)

    # Fail - missing edit permission.
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" "${attachReq}")" = "Forbidden" ]

    # Succeed - with edit permission.
    lxc auth group permission add "${authGroup}" instance "${inst}" can_edit project="${project}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" "${attachReq}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst}" | jq --exit-status '.devices."vol-01".source == "vol-01"'

    # Detach device.
    detachReq='{
    "devices": {
        "vol-01": null
    }
}'

    etag=$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get-etag "${inst}")
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" "${detachReq}" "${etag}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst}" | jq --exit-status '.devices == {}'

    # Manage device on a different instance.
    inst2="${inst}-2"
    # Use a container for the second instance as it is faster.
    lxc launch testimage "${inst2}" --project "${project}" --storage "${pool}"

    # Fail - missing permission.
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst2}")" = "Not Found" ]
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst2}" "${attachReq}")" = "Not Found" ]

    # Succeed - with edit permissions.
    lxc auth group permission add "${authGroup}" instance "${inst2}" can_edit project="${project}"
    etag=$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get-etag "${inst2}")
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst2}" "${attachReq}" "${etag}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst2}" | jq --exit-status '.devices."vol-01".source == "vol-01"'

    etag=$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get-etag "${inst2}")
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst2}" "${detachReq}" "${etag}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst2}" | jq --exit-status '.devices == {}'

    lxc delete "${inst2}" --project "${project}" --force

    # Use existing volume owned by the identity as a source for the new volume.
    vol3=$(cat <<EOF
{
    "name": "vol-03",
    "type": "custom",
    "pool": "${pool}",
    "source": {
        "name": "vol-01",
        "pool": "${pool}",
        "type": "copy"
    }
}
EOF
)

    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${vol3}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" custom vol-03

    # Ensure creating a volume from a source volume not owned by the identity fails.
    lxc storage volume create "${pool}" unowned size=8MiB

    volUnownedSource=$(cat <<EOF
{
    "name": "vol-unowned",
    "type": "custom",
    "pool": "${pool}",
    "source": {
        "name": "unowned",
        "pool": "${pool}",
        "type": "copy"
    }
}
EOF
)

    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${volUnownedSource}")" = "Source volume not found" ]
    lxc storage volume delete "${pool}" unowned

    # Delete storage volume (fail - insufficient permissions).
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" "${instType}" "${inst}")" = "Forbidden" ]

    # Grant storage volume delete permission.
    lxc auth group permission add "${authGroup}" project "${project}" can_delete_storage_volumes

    # Delete storage volume (fail - non-custom volume).
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" "${instType}" "${inst}")" = "Only custom storage volume requests are allowed" ]

    # Delete storage volumes.
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" custom non-existing-volume)" = "Storage volume not found" ]
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" custom vol-01
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" custom vol-02
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" custom vol-03

    # Ensure storage volumes are deleted.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage volumes "${pool}" | jq --exit-status '. == []'

    # Test block volumes (VMs only).
    if [ "${instType}" = "virtual-machine" ]; then
      # Create a custom block volume.
      volBlock='{"name": "block-vol", "type": "custom", "content_type": "block", "config": {"size": "8MiB"}}'
      lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${volBlock}"

      # Attach block volume to the instance.
      attachReq=$(cat <<EOF
{
    "devices": {
        "block-vol": {
            "type": "disk",
            "pool": "${pool}",
            "source": "block-vol"
        }
    }
}
EOF
)

      lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" "${attachReq}"
      lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance get "${inst}" | jq --exit-status '.devices."block-vol".source == "block-vol"'

      # Try increasing block volume size while the volume is attached to a running VM (in use).
      # Ensure the returned status code is 423 (StatusLocked).
      patchReq='{"config": {"size": "12MiB"}}'
      opID="$(lxc exec "${inst}" --project "${project}" -- curl -s --unix-socket /dev/lxd/sock -H "Authorization: Bearer ${token}" -X PATCH "lxd/1.0/storage-pools/${pool}/volumes/custom/block-vol" -d "${patchReq}" | jq -r .id)"
      [ "$(lxc exec "${inst}" --project "${project}" -- curl -s -o /dev/null -w "%{http_code}" --unix-socket /dev/lxd/sock -H "Authorization: Bearer ${token}" -X GET "lxd/1.0/operations/${opID}/wait?timeout=5" -d "${patchReq}")" = "423" ]

      # Detach device.
      detachReq='{"devices": {"block-vol": null}}'
      lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" "${detachReq}"

      # Delete volume.
      lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" custom block-vol
    fi

    # Cleanup.
    if [ "${instType}" = "virtual-machine" ]; then
      lxc image delete "$(lxc config get "${inst}" volatile.base_image --project "${project}")" --project "${project}"
    fi
    lxc delete "${inst}" --project "${project}" --force
    lxc auth identity delete "${authIdentity}"
    lxc auth group delete "${authGroup}"
  done

  # Cleanup.
  lxc storage delete "${pool}"
  if [ "${project}" != "default" ]; then
      lxc project delete "${project}"
  fi
}

test_devlxd_volume_management_snapshots() {
  local testName="devlxd-snapshot-mgmt"

  local instPrefix="${testName}"
  local project="${testName}"
  local authGroup="${testName}-group"
  local authIdentity="devlxd/${testName}-identity"

  pool=$(lxc profile device get default root pool)

  if [ "${project}" != "default" ]; then
    lxc project create "${project}" --config features.images=false
  fi

  local instTypes="container"
  ensure_import_testimage

  if [ "${LXD_VM_TESTS}" != "0" ]; then
    instTypes="${instTypes} virtual-machine"
    ensure_import_ubuntu_vm_image
  fi

  for instType in $instTypes; do
    inst="${instPrefix}-${instType}"

    image="testimage"
    opts="--storage ${pool} --config security.devlxd.management.volumes=true"
    if [ "${instType}" = "virtual-machine" ]; then
        image="ubuntu-vm"
        opts="$opts --vm --config limits.memory=384MiB --device ${SMALL_VM_ROOT_DISK}"

        orig_volume_size="$(lxc storage get "${pool}" volume.size)"
        if [ -n "${orig_volume_size:-}" ]; then
          echo "==> Override the volume.size to accommodate a large VM"
          lxc storage set "${pool}" volume.size "${SMALLEST_VM_ROOT_DISK}"
        fi
    fi

    # shellcheck disable=SC2086 # Variable "opts" must not be quoted, we want word splitting.
    lxc launch "${image}" "${inst}" $opts --project "${project}"
    waitInstanceReady "${inst}" "${project}"

    # Install devlxd-client and make sure it works.
    lxc file push --project "${project}" --quiet "$(command -v devlxd-client)" "${inst}/bin/"
    lxc exec --project "${project}" "${inst}" -- devlxd-client

    # Create identity with all volume management permissions.
    lxc auth group create "${authGroup}"
    lxc auth identity create "${authIdentity}" --group "${authGroup}"
    token=$(lxc auth identity token issue "${authIdentity}" --quiet)

    # Grant permissions.
    lxc auth group permission add "${authGroup}" project "${project}" can_view
    lxc auth group permission add "${authGroup}" project "${project}" storage_volume_manager
    lxc auth group permission add "${authGroup}" project "${project}" instance_manager

    # Create a custom storage volume.
    vol1='{
    "name": "vol-01",
    "type": "custom",
    "config": {
        "size": "8MiB"
    }
}'

    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${vol1}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-volume "${pool}" custom vol-01

    # Create snapshots with custom and auto-generated names.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-snapshot "${pool}" custom vol-01 "{}"
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-snapshot "${pool}" custom vol-01 '{"name": "my-snap"}'
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-snapshot "${pool}" custom non-existing "{}")" = "Storage volume not found" ]

    # Fetch snapshots.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-snapshot "${pool}" custom vol-01 snap0
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-snapshot "${pool}" custom vol-01 my-snap
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage get-snapshot "${pool}" custom vol-01 non-existing)" = "Storage volume not found" ]

    # List snapshots.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage snapshots "${pool}" custom vol-01 | jq --exit-status 'length == 2'
    [ "$(lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage snapshots "${pool}" custom non-existing)" = "Storage volume not found" ]

    # Test volume creation from a snapshot.
    attachReq=$(cat <<EOF
{
    "devices": {
        "vol-01": {
            "type": "disk",
            "pool": "${pool}",
            "source": "vol-01",
            "path": "/mnt/vol-01"
        }
    }
}
EOF
)

    # Attach vol-01 to the instance.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" "${attachReq}"

    # Write some data to the volume.
    echo "initial-content" | lxc file push --project "${project}" - "${inst}/mnt/vol-01/test.txt"
    [ "$(lxc exec "${inst}" --project "${project}" -- cat /mnt/vol-01/test.txt)" = "initial-content" ]

    # Make snapshot.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-snapshot "${pool}" custom vol-01 '{"name": "snapXX"}'

    # Modify data on the volume.
    echo "modified-content" | lxc file push --project "${project}" - "${inst}/mnt/vol-01/test.txt"
    [ "$(lxc exec "${inst}" --project "${project}" -- cat /mnt/vol-01/test.txt)" = "modified-content" ]

    # Use existing snapshot as a source for the new volume.
    volFromSnapshot=$(cat <<EOF
{
    "name": "vol-from-snapshot",
    "type": "custom",
    "pool": "${pool}",
    "source": {
        "name": "vol-01/snapXX",
        "pool": "${pool}",
        "type": "copy"
    }
}
EOF
)

    # Create new volume from snapshot.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage create-volume "${pool}" "${volFromSnapshot}"

    attachReq=$(cat <<EOF
{
    "devices": {
        "vol-from-snapshot": {
            "type": "disk",
            "pool": "${pool}",
            "source": "vol-from-snapshot",
            "path": "/mnt/vol"
        }
    }
}
EOF
)

    # Attach copied volume to the instance.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" "${attachReq}"

    # Read data from the volume.
    [ "$(lxc file pull --project "${project}" "${inst}/mnt/vol/test.txt" -)" = "initial-content" ]

    # Detach copied volume from the instance.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" '{"devices":{"vol-from-snapshot": null}}'

    # Detach vol-01 from the instance.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client instance update "${inst}" '{"devices": {"vol-01": null}}'

    # Delete snapshots.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-snapshot "${pool}" custom vol-01 snapXX
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage snapshots "${pool}" custom vol-01 | jq --exit-status 'length == 2'

    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-snapshot "${pool}" custom vol-01 my-snap
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage snapshots "${pool}" custom vol-01 | jq --exit-status 'length == 1'

    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-snapshot "${pool}" custom vol-01 snap0
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage snapshots "${pool}" custom vol-01 | jq --exit-status 'length == 0'

    # Delete storage volume.
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" custom vol-01
    lxc exec "${inst}" --project "${project}" --env DEVLXD_BEARER_TOKEN="${token}" -- devlxd-client storage delete-volume "${pool}" custom vol-from-snapshot

    # Cleanup.
    imageFingerprint="$(lxc config get "${inst}" volatile.base_image --project "${project}")"
    lxc delete "${inst}" --project "${project}" --force
    lxc image delete "${imageFingerprint}" --project "${project}"
    lxc auth identity delete "${authIdentity}"
    lxc auth group delete "${authGroup}"

    if [ -n "${orig_volume_size:-}" ]; then
      echo "==> Restore the volume.size"
      lxc storage set "${pool}" volume.size "${orig_volume_size}"
    fi
  done

  # Cleanup.
  if [ "${project}" != "default" ]; then
      lxc project delete "${project}"
  fi
}

test_devlxd_vm() {
  pool="lxdtest-$(basename "${LXD_DIR}")"
  orig_volume_size="$(lxc storage get "${pool}" volume.size)"
  if [ -n "${orig_volume_size:-}" ]; then
    echo "==> Override the volume.size to accommodate a large VM"
    lxc storage set "${pool}" volume.size "${SMALLEST_VM_ROOT_DISK}"
  fi

  ensure_import_ubuntu_vm_image

  lxc init ubuntu-vm v1 --vm -c agent.nic_config=true -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"
  lxc config device add v1 a-nic nic nictype=p2p name=a-nic mtu=1400
  lxc start v1
  waitInstanceReady v1

  setup_lxd_agent_gocoverage v1

  echo "==> Check that devlxd is enabled by default and works"
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/devices | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/config | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'instance-id:'
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'local-hostname:'

  # Run sync before forcefully restarting the VM otherwise the filesystem will be corrupted.
  lxc exec v1 -- "sync"

  # Coverage data requires clean shutdown
  if coverage_enabled; then
    # Errors are possible if the service is stopped before the exec completes
    # as it kills the communication channel
    lxc exec v1 -- systemctl stop --no-block lxd-agent.service || true
  fi

  lxc restart -f v1
  waitInstanceReady v1

  echo "==> Confirm agent.nic_config applied the NIC configuration"
  lxc exec v1 -- ip link show a-nic | grep -wF "mtu 1400"

  echo "==> Remove the NIC"
  lxc config device remove v1 a-nic
  ! lxc exec v1 -- ip link show a-nic || false

  echo "==> Check that devlxd is working after a restart"
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/devices | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/config | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'instance-id:'
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'local-hostname:'

  echo "==> Check that devlxd is not working once disabled"
  lxc config set v1 security.devlxd false
  ! lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 || false

  echo "==> Check that devlxd can be enabled live"
  lxc config set v1 security.devlxd true
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/devices | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/config | jq

  echo "==> Ensure that the output metadata is in correct format"
  META_DATA="$(lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data)"
  [ "$(grep -cxE 'instance-id: [^ ]{36}|local-hostname: v1' <<< "${META_DATA}" || echo fail)" = "2" ]
  [ "$(wc -l <<< "${META_DATA}" || echo fail)" = "2" ]

  echo "==> Test cloud-init user-data"
  # Ensure the header is preserved and the output value is not escaped.
  cloudInitUserData='#cloud-config
package_update: false
package_upgrade: false
runcmd:
- echo test'

  lxc config set v1 cloud-init.user-data "${cloudInitUserData}"
  out="$(lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock lxd/1.0/config/cloud-init.user-data)"
  [ "${out}" = "${cloudInitUserData}" ]
  lxc config unset v1 cloud-init.user-data

  echo "===> Test instance Ready state"
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock -X PATCH -d '{"state":"Ready"}' http://custom.socket/1.0
  [ "$(lxc config get v1 volatile.last_state.ready)" = "true" ]

  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock -X PATCH -d '{"state":"Started"}' http://custom.socket/1.0
  [ "$(lxc config get v1 volatile.last_state.ready)" = "false" ]

  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock -X PATCH -d '{"state":"Ready"}' http://custom.socket/1.0
  [ "$(lxc config get v1 volatile.last_state.ready)" = "true" ]

  # If gathering coverage data, the lxd-agent.service needs to be stopped
  # cleanly to allow the coverage file to be flushed.
  teardown_lxd_agent_gocoverage v1

  lxc stop -f v1
  [ "$(lxc config get v1 volatile.last_state.ready)" = "false" ]

  # TODO: add nested virt part from lxd-ci test

  # Cleanup
  lxc image delete "$(lxc config get v1 volatile.base_image)"
  lxc delete v1
  if [ -n "${orig_volume_size:-}" ]; then
    echo "==> Restore the volume.size"
    lxc storage set "${pool}" volume.size "${orig_volume_size}"
  fi
}
