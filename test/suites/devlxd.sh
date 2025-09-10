test_devlxd() {
  ensure_import_testimage
  fingerprint="$(lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  # Ensure testimage is not set as cached.
  lxd sql global "UPDATE images SET cached=0 WHERE fingerprint=\"${fingerprint}\""

  lxc launch testimage devlxd -c security.devlxd=false

  ! lxc exec devlxd -- test -S /dev/lxd/sock || false
  lxc config unset devlxd security.devlxd
  lxc exec devlxd -- test -S /dev/lxd/sock
  lxc file push --quiet "$(command -v devlxd-client)" devlxd/bin/

  ### Test bearer token authentication

  # Check that auth is untrusted by default
  lxc exec devlxd -- devlxd-client get-state | jq -e '.auth == "untrusted"'

  # Create a bearer identity and issue a token
  lxc auth identity create devlxd/foo
  devlxd_token1="$(lxc auth identity token issue devlxd/foo --quiet)"

  # Check that the token is valid (devlxd can be called with the token and auth is trusted).
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token1}" devlxd -- devlxd-client get-state | jq -e '.auth == "trusted"'

  # Issue another token, the old token should be invalid (so devlxd calls fail) and the new one valid.
  devlxd_token2="$(lxc auth identity token issue devlxd/foo --quiet)"
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token1}" devlxd -- devlxd-client get-state || false)" = 'Failed to verify bearer token: Token is not valid: token signature is invalid: signature is invalid' ]
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token2}" devlxd -- devlxd-client get-state | jq -e '.auth == "trusted"'

  # Revoke the token, it should no longer be valid.
  subject="$(lxc query /1.0/auth/identities/bearer/foo | jq -r .id)"
  lxc auth identity token revoke devlxd/foo
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token2}" devlxd -- devlxd-client get-state || false)" = "Failed to verify bearer token: Identity \"${subject}\" (bearer) not found" ]

  # Issue a new token, it should be valid
  devlxd_token3="$(lxc auth identity token issue devlxd/foo --quiet)"
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token3}" devlxd -- devlxd-client get-state | jq -e '.auth == "trusted"'

  # Delete the identity, the token should no longer be valid.
  lxc auth identity delete devlxd/foo
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token3}" devlxd -- devlxd-client get-state || false)" = "Failed to verify bearer token: Identity \"${subject}\" (bearer) not found" ]

  # Create a token with an expiry
  lxc auth identity create devlxd/foo
  devlxd_token4="$(lxc auth identity token issue devlxd/foo --quiet --expiry 2S)"

  # It's initially valid
  lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token4}" devlxd -- devlxd-client get-state | jq -e '.auth == "trusted"'

  # It's not valid after the expiry
  sleep 3
  [ "$(! lxc exec --env DEVLXD_BEARER_TOKEN="${devlxd_token4}" devlxd -- devlxd-client get-state || false)" = 'Failed to verify bearer token: Token is not valid: token has invalid claims: token is expired' ]

  # Clean up
  lxc auth identity delete devlxd/foo

  # No secret remains in the database after the identity was deleted
  [ "$(lxd sql global --format csv 'SELECT COUNT(*) FROM secrets WHERE entity_id = (SELECT id FROM identities WHERE name = "foo")')" = 0 ]

  # Try to get a host's private image from devlxd.
  [ "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}")" = "Forbidden" ]
  lxc config set devlxd security.devlxd.images true
  # Trying to get a private image should return a not found error so that the client can't infer the existence
  # of an image with the provided fingerprint.
  [ "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}")" = "Not Found" ]
  lxd sql global "UPDATE images SET cached=1 WHERE fingerprint=\"${fingerprint}\""
  # No output means the export succeeded.
  [ -z "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}")" ]

  lxc config set devlxd user.foo bar
  [ "$(lxc exec devlxd -- devlxd-client user.foo)" = "bar" ]

  lxc config set devlxd user.foo "bar %s bar"
  [ "$(lxc exec devlxd -- devlxd-client user.foo)" = "bar %s bar" ]

  # Make sure instance configuration keys are not accessible
  [ "$(lxc exec devlxd -- devlxd-client security.nesting)" = "Forbidden" ]
  lxc config set devlxd security.nesting true
  [ "$(lxc exec devlxd -- devlxd-client security.nesting)" = "Forbidden" ]

  cmd=$(unset -f lxc; command -v lxc)
  ${cmd} exec devlxd -- devlxd-client monitor-websocket > "${TEST_DIR}/devlxd-websocket.log" &
  client_websocket=$!

  ${cmd} exec devlxd -- devlxd-client monitor-stream > "${TEST_DIR}/devlxd-stream.log" &
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
    lxc config set devlxd user.foo bar
    lxc config set devlxd security.nesting true

    true > "${TEST_DIR}/devlxd-websocket.log"
    true > "${TEST_DIR}/devlxd-stream.log"

    lxc config set devlxd user.foo baz
    lxc config set devlxd security.nesting false
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

  kill -9 "${monitorDevlxdPID}" || true

  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  # volatile.last_state.ready should be unset during daemon init
  [ -z "$(lxc config get devlxd volatile.last_state.ready)" ]

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
  [ "$(lxc exec devlxd -- devlxd-client devices | jq -r .eth0.hwaddr)" = "${hwaddr}" ]

  lxc delete devlxd --force
  kill -9 "${monitorDevlxdPID}" || true

  [ "${MATCH}" = "1" ]
}

test_devlxd_volume_management() {
  local testName="devlxd-volume-mgmt"

  local instPrefix="${testName}"
  local instTypes="container" # "container vm" - VMs are currently not supported in LXD test suite.
  local pool="${testName}"
  local project="${testName}"

  ensure_import_testimage
  poolDriver="$(storage_backend "$LXD_DIR")"

  lxc storage create "${pool}" "${poolDriver}"
  if [ "${project}" != "default" ]; then
    lxc project create "${project}" --config features.images=false
  fi

  for instType in $instTypes; do
    inst="${instPrefix}-${instType}"

    opts=""
    if [ "${instType}" = "vm" ]; then
        opts="--vm"
    fi

    # shellcheck disable=SC2248
    lxc launch testimage "${inst}" $opts \
        --project "${project}" \
        --storage "${pool}"

    # Install devlxd-client and make sure it works.
    lxc file push --project "${project}" --quiet "$(command -v devlxd-client)" "${inst}/bin/"
    lxc exec --project "${project}" "${inst}" -- devlxd-client

    # Ensure supported storage drivers are included in /1.0 only when volume management security flag is enabled.
    lxc exec "${inst}" --project "${project}" -- devlxd-client get-state | jq -e '.supported_storage_drivers | length == 0'
    lxc config set "${inst}" --project "${project}" security.devlxd.management.volumes=true
    lxc exec "${inst}" --project "${project}" -- devlxd-client get-state | jq -e '.supported_storage_drivers | length > 0'
    lxc exec "${inst}" --project "${project}" -- devlxd-client get-state | jq -e '.supported_storage_drivers[] | select(.name == "dir") | .remote == false'

    # Cleanup.
    lxc delete "${inst}" --project "${project}" --force
  done

  # Cleanup.
  lxc storage delete "${pool}"
  if [ "${project}" != "default" ]; then
      lxc project delete "${project}"
  fi
}
