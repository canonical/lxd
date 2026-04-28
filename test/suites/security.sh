test_security() {
  ensure_import_testimage

  # CVE-2016-1581
  if [ "$(storage_backend "$LXD_DIR")" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    spawn_lxd "${LXD_INIT_DIR}" false

    ZFS_POOL="lxdtest-$(basename "${LXD_DIR}")-init"
    LXD_DIR=${LXD_INIT_DIR} lxd init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

    PERM=$(stat -c %a "${LXD_INIT_DIR}/disks/${ZFS_POOL}.img")
    if [ "${PERM}" != "600" ]; then
      echo "Bad zfs.img permissions: ${PERM}"
      false
    fi

    kill_lxd "${LXD_INIT_DIR}"
  fi

  # CVE-2016-1582
  lxc launch testimage test-priv -c security.privileged=true -d "${SMALL_ROOT_DISK}"

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-priv")
  FUID=$(stat -L -c %u "${LXD_DIR}/containers/test-priv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${FUID}" != "0" ]; then
    echo "Bad container owner: ${FUID}"
    false
  fi

  lxc config set test-priv security.privileged false
  lxc restart test-priv --force
  lxc config set test-priv security.privileged true
  lxc restart test-priv --force

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-priv")
  FUID=$(stat -L -c %u "${LXD_DIR}/containers/test-priv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${FUID}" != "0" ]; then
    echo "Bad container owner: ${FUID}"
    false
  fi

  lxc delete test-priv --force

  lxc launch testimage test-unpriv -d "${SMALL_ROOT_DISK}"
  lxc config set test-unpriv security.privileged true
  lxc restart test-unpriv --force

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-unpriv")
  FUID=$(stat -L -c %u "${LXD_DIR}/containers/test-unpriv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${FUID}" != "0" ]; then
    echo "Bad container owner: ${FUID}"
    false
  fi

  lxc config set test-unpriv security.privileged false
  lxc restart test-unpriv --force

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-unpriv")
  FUID=$(stat -L -c %u "${LXD_DIR}/containers/test-unpriv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${FUID}" = "0" ]; then
    echo "Bad container owner: ${FUID}"
    false
  fi

  lxc delete test-unpriv --force

  local LXD_STORAGE_DIR

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  # Enforce that only unprivileged containers can be created
  LXD_UNPRIVILEGED_ONLY=true
  export LXD_UNPRIVILEGED_ONLY
  spawn_lxd "${LXD_STORAGE_DIR}" true
  unset LXD_UNPRIVILEGED_ONLY

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # Verify that no privileged container can be created
    ! lxc init --empty c1 -d "${SMALL_ROOT_DISK}" -c security.privileged=true || false

    # Verify that unprivileged container can be created
    lxc init --empty c1 -d "${SMALL_ROOT_DISK}"

    # Verify that we can't be tricked into using privileged containers
    ! lxc config set c1 security.privileged true || false
    ! lxc config set c1 raw.idmap "both 0 1000" || false
    ! lxc config set c1 raw.lxc "lxc.idmap=" || false
    ! lxc config set c1 raw.lxc "lxc.include=" || false

    # Verify that we can still unset and set to security.privileged to "false"
    lxc config set c1 security.privileged false
    lxc config unset c1 security.privileged

    # Verify that a profile can't be changed to trick us into using privileged
    # containers
    ! lxc profile set default security.privileged true || false
    ! lxc profile set default raw.idmap "both 0 1000" || false
    ! lxc profile set default raw.lxc "lxc.idmap=" || false
    ! lxc profile set default raw.lxc "lxc.include=" || false

    # Verify that we can still unset and set to security.privileged to "false"
    lxc profile set default security.privileged false
    lxc profile unset default security.privileged

    lxc delete c1
  )

  # shellcheck disable=SC2031,2269
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}

test_security_protection() {
  ensure_import_testimage

  # Test deletion protecton
  lxc profile set default security.protection.delete true

  lxc init --empty c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1
  lxc delete c1/snap0
  ! lxc delete c1 || false

  lxc config set c1 security.protection.delete false
  lxc delete c1

  lxc profile unset default security.protection.delete

  # Test start protection
  lxc profile set default security.protection.start true

  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
  ! lxc start c1 || false

  lxc config set c1 security.protection.start false
  lxc start c1
  lxc delete c1 --force

  lxc profile unset default security.protection.start

  # Test shifting protection

  # Respawn LXD with kernel ID shifting support disabled to force manual shifting.
  shutdown_lxd "${LXD_DIR}"
  lxdIdmappedMountsDisable=${LXD_IDMAPPED_MOUNTS_DISABLE:-}

  export LXD_IDMAPPED_MOUNTS_DISABLE=1
  respawn_lxd "${LXD_DIR}" true

  lxc launch testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc stop c1 --force

  lxc profile set default security.protection.shift true
  lxc start c1
  lxc stop c1 --force

  lxc publish c1 --alias=protected
  lxc image delete protected

  lxc snapshot c1
  lxc publish c1/snap0 --alias=protected
  lxc image delete protected

  lxc config set c1 security.privileged true
  ! lxc start c1 || false
  lxc config set c1 security.protection.shift false
  lxc start c1
  lxc delete c1 --force

  lxc profile unset default security.protection.shift

  # Respawn LXD to restore default kernel shifting support.
  shutdown_lxd "${LXD_DIR}"
  export LXD_IDMAPPED_MOUNTS_DISABLE="${lxdIdmappedMountsDisable}"

  respawn_lxd "${LXD_DIR}" true
}

test_security_events() {
  sub_test "Verify event_security API extension is present"
  lxc query /1.0 | jq -e '.api_extensions | contains(["event_security"])'

  sub_test "Verify lxc monitor --type=security connects without error"
  # No security events are emitted yet (emission sites are a later subtask),
  # so only the connection establishment is verified here.
  local monfile="${TEST_DIR}/security-events.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!

  # The monitor process exits immediately on connection error. Retry kill -0
  # for up to 10 seconds; once it succeeds we know the connection is live.
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done

  kill -0 "${mon_pid}"

  kill_go_proc "${mon_pid}" || true
  rm -f "${monfile}"

  sub_test "Verify existing lifecycle events are unaffected by the security event type"
  ensure_import_testimage
  local monfile_lifecycle="${TEST_DIR}/lifecycle-events.jsonl"
  lxc monitor --type=lifecycle --format=json > "${monfile_lifecycle}" &
  local mon_lifecycle_pid=$!
  sleep 0.1

  lxc init --empty c-security-event-test
  lxc delete c-security-event-test --force

  # Retry for up to 5 seconds for the lifecycle event to appear in the monitor file
  # before killing the monitor, so the file contents are reliably complete.
  for _ in $(seq 5); do
    jq --exit-status --slurp 'map(select(.type == "lifecycle" and .metadata.action == "instance-created")) | length == 1' "${monfile_lifecycle}" && break
    sleep 1
  done

  kill_go_proc "${mon_lifecycle_pid}" || true

  jq --exit-status --slurp 'map(select(.type == "lifecycle" and .metadata.action == "instance-created")) | length == 1' "${monfile_lifecycle}"

  rm -f "${monfile_lifecycle}"
}

test_security_events_bearer_authn() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Create a bearer identity and capture the issued token. No group is needed:
  # an authenticated identity can hit /1.0 without any entitlement on server,
  # which is enough to drive the trusted-then-revoked flow under test.
  lxc auth identity create bearer/security-events-bearer
  local bearer_token
  bearer_token="$(lxc auth identity token issue bearer/security-events-bearer --quiet)"

  # Subscribe to the security stream before any auth attempt so the revocation
  # path's authn_token_reuse event lands in the file.
  local monfile="${TEST_DIR}/security-events-bearer.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done
  kill -0 "${mon_pid}"

  sub_test "Verify a fresh bearer token authenticates successfully"
  curl --silent --insecure --header "Authorization: Bearer ${bearer_token}" "https://${LXD_ADDR}/1.0" | jq --exit-status '.metadata.auth == "trusted"'

  sub_test "Verify reuse of a revoked bearer token is denied and audited"
  lxc auth identity token revoke bearer/security-events-bearer
  # A revoked token causes bearer.Authenticate to return an error, which the
  # daemon translates to a 403 Forbidden response.
  curl --silent --insecure --header "Authorization: Bearer ${bearer_token}" "https://${LXD_ADDR}/1.0" | jq --exit-status '.error_code == 403'

  # Poll the monitor file (up to 10 s) for the authn_token_reuse event raised
  # by bearer.Authenticate when verifyToken fails against the rotated secret.
  for _ in $(seq 10); do
    jq --exit-status --slurp 'map(select(.type == "security" and (.metadata.name | startswith("authn_token_reuse")))) | length >= 1' "${monfile}" && break
    sleep 1
  done

  jq --exit-status --slurp 'map(select(.type == "security" and (.metadata.name | startswith("authn_token_reuse")))) | length >= 1' "${monfile}"
  jq --exit-status --slurp 'map(select(.type == "security" and (.metadata.name | startswith("authn_token_reuse")))) | .[0] | (.metadata.level == "warning") and (.metadata.requestor.protocol != "") and (.metadata.requestor.username != "") and (.metadata.request_path == "/1.0") and (.metadata.requestor.address != "")' "${monfile}"

  kill_go_proc "${mon_pid}" || true
  rm -f "${monfile}"

  lxc auth identity delete bearer/security-events-bearer
}
