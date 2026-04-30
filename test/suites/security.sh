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

test_security_sys_events() {
  # Spawn a dedicated LXD daemon so the startup and shutdown cycle is
  # isolated from the shared per-suite daemon.
  local LXD_SYS_DIR
  LXD_SYS_DIR="$(mktemp --directory --tmpdir="${TEST_DIR}" XXX)"
  spawn_lxd "${LXD_SYS_DIR}" false

  local loki_log="${TEST_DIR}/loki.logs"
  kill_loki
  rm -f "${loki_log}"
  spawn_loki

  # sys_startup is broadcast at the end of Daemon.Start before any
  # /1.0/events client can subscribe, so Loki, whose client is
  # initialised from persisted config at startup, is the only sink that
  # can witness it.
  LXD_DIR="${LXD_SYS_DIR}" lxc config set \
    loki.api.url=http://127.0.0.1:3100 \
    loki.auth.username=loki \
    loki.auth.password=pass \
    loki.types=security

  sub_test "Verify sys_startup is forwarded to Loki on daemon start"
  shutdown_lxd "${LXD_SYS_DIR}"
  respawn_lxd "${LXD_SYS_DIR}" true

  for _ in $(seq 10); do
    jq --exit-status '.streams[].values[][1] | fromjson | select(.event == "sys_startup")' "${loki_log}" && break
    sleep 1
  done

  jq --exit-status '.streams[].values[][1] | fromjson | select(.event == "sys_startup") | (.level == "info") and (.description == "LXD daemon started") and (.useragent == null)' "${loki_log}"

  sub_test "Verify sys_shutdown fires when the daemon stops"
  # sys_shutdown is broadcast inside Daemon.Stop before the event server
  # is torn down. A pre-subscribed lxc monitor reliably captures it
  # because the WebSocket flushes pending frames before close.
  local monfile="${TEST_DIR}/security-sys-shutdown.jsonl"
  LXD_DIR="${LXD_SYS_DIR}" lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done
  kill -0 "${mon_pid}"

  shutdown_lxd "${LXD_SYS_DIR}"

  for _ in $(seq 10); do
    jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "sys_shutdown")) | length >= 1' "${monfile}" && break
    sleep 1
  done

  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "sys_shutdown")) | .[0] | (.metadata.level == "info") and (.metadata.description == "LXD daemon stopping") and (.metadata | has("requestor") | not)' "${monfile}"

  kill_go_proc "${mon_pid}" || true
  kill_lxd "${LXD_SYS_DIR}"
  kill_loki
  rm -f "${loki_log}" "${monfile}"
}

test_security_user_events() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  local monfile="${TEST_DIR}/security-user-events.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done
  kill -0 "${mon_pid}"

  local lifecycle_monfile="${TEST_DIR}/security-user-events-lifecycle.jsonl"
  lxc monitor --type=lifecycle --format=json > "${lifecycle_monfile}" &
  local lifecycle_mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${lifecycle_mon_pid}" && break
    sleep 1
  done
  kill -0 "${lifecycle_mon_pid}"

  sub_test "Verify user_created fires when a bearer identity is created"
  lxc auth identity create bearer/security-user-events-bearer

  for _ in $(seq 10); do
    jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_created")) | length >= 1' "${monfile}" && break
    sleep 1
  done

  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_created")) | length == 1' "${monfile}"
  # OWASP-required fields populated by the request middleware: caller protocol
  # and address (lxc client uses TLS over the local socket), the request URI,
  # and the HTTP method.
  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_created")) | .[0] | (.metadata.level == "info") and (.metadata.description == "Identity created") and (.metadata.request_method == "POST") and (.metadata.request_path == "/1.0/auth/identities/bearer") and (.metadata.requestor.protocol != "") and (.metadata.requestor.address != "")' "${monfile}"

  sub_test "Verify user_updated fires when a bearer identity is modified"
  lxc auth group create security-user-events-group
  lxc auth identity group add bearer/security-user-events-bearer security-user-events-group

  for _ in $(seq 10); do
    jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_updated")) | length >= 1' "${monfile}" && break
    sleep 1
  done

  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_updated")) | length == 1' "${monfile}"
  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_updated")) | .[0] | (.metadata.level == "info") and (.metadata.description == "Identity updated") and (.metadata.requestor.protocol != "")' "${monfile}"

  sub_test "Verify user_deleted fires when a bearer identity is removed"
  lxc auth identity delete bearer/security-user-events-bearer

  for _ in $(seq 10); do
    jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_deleted")) | length >= 1' "${monfile}" && break
    sleep 1
  done

  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_deleted")) | length == 1' "${monfile}"
  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_deleted")) | .[0] | (.metadata.level == "info") and (.metadata.description == "Identity deleted") and (.metadata.request_method == "DELETE")' "${monfile}"

  sub_test "Verify lifecycle and security events coexist 1:1 with no duplication"
  # Each of the three actions raised exactly one lifecycle event and one
  # security event; cross-stream counts must match. Bearer identities
  # are addressed in lifecycle URLs by their generated UUID, not their
  # friendly name, so match the source by prefix.
  jq --exit-status --slurp 'map(select(.type == "lifecycle" and .metadata.action == "identity-created" and (.metadata.source | startswith("/1.0/auth/identities/bearer/")))) | length == 1' "${lifecycle_monfile}"
  jq --exit-status --slurp 'map(select(.type == "lifecycle" and .metadata.action == "identity-updated" and (.metadata.source | startswith("/1.0/auth/identities/bearer/")))) | length == 1' "${lifecycle_monfile}"
  jq --exit-status --slurp 'map(select(.type == "lifecycle" and .metadata.action == "identity-deleted" and (.metadata.source | startswith("/1.0/auth/identities/bearer/")))) | length == 1' "${lifecycle_monfile}"

  kill_go_proc "${mon_pid}" || true
  kill_go_proc "${lifecycle_mon_pid}" || true

  lxc auth group delete security-user-events-group

  rm -f "${monfile}" "${lifecycle_monfile}"
}

test_security_user_events_oidc() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  spawn_oidc
  set_oidc test-user-security test-user-security@example.com
  lxc config set "oidc.issuer=http://127.0.0.1:$(< "${TEST_DIR}/oidc.port")/" "oidc.client.id=device"

  local monfile="${TEST_DIR}/security-oidc-user-events.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done
  kill -0 "${mon_pid}"

  sub_test "Verify user_created fires on OIDC first login"
  BROWSER=curl lxc remote add --accept-certificate oidc-security "${LXD_ADDR}" --auth-type oidc

  lxc query oidc-security:/1.0 | jq --exit-status '.auth == "trusted"'

  for _ in $(seq 10); do
    jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_created" and .metadata.request_path == "/1.0")) | length >= 1' "${monfile}" && break
    sleep 1
  done

  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "user_created" and .metadata.request_path == "/1.0")) | length == 1' "${monfile}"

  sub_test "Verify user_created does not fire on subsequent OIDC logins"
  # Drop the cached cookie so the next request triggers a fresh auth flow.
  rm -f "${LXD_CONF}/jars/oidc-security"

  local before_count
  before_count="$(jq --slurp 'map(select(.type == "security" and .metadata.name == "user_created" and .metadata.request_path == "/1.0")) | length' "${monfile}")"

  BROWSER=curl lxc query oidc-security:/1.0 | jq --exit-status '.auth == "trusted"'

  # Allow a short window for any (unwanted) duplicate event to land.
  sleep 2

  local after_count
  after_count="$(jq --slurp 'map(select(.type == "security" and .metadata.name == "user_created" and .metadata.request_path == "/1.0")) | length' "${monfile}")"
  [ "${before_count}" = "${after_count}" ]

  kill_go_proc "${mon_pid}" || true
  rm -f "${monfile}"

  lxc auth identity delete oidc/test-user-security@example.com
  lxc remote remove oidc-security
  lxc config set oidc.issuer="" oidc.client.id=""
  kill_oidc
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

test_authn_events() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Helper: poll the JSONL monitor file (up to 10 s) for jq_filter, then kill
  # the monitor, assert one final time, and remove the file.
  _wait_authn_event() {
    local monfile="${1}" mon_pid="${2}" jq_filter="${3}"
    for _ in $(seq 10); do
      jq --exit-status --slurp "${jq_filter}" "${monfile}" && break
      sleep 1
    done
    kill_go_proc "${mon_pid}" || true
    jq --exit-status --slurp "${jq_filter}" "${monfile}"
    rm -f "${monfile}"
  }

  sub_test "Verify authn_login_fail does not fire on public unauthenticated endpoints"
  local monfile="${TEST_DIR}/authn-no-event.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!
  sleep 0.2

  # GET /1.0 has AllowUntrusted=true so daemon.Authenticate is not reached
  # with a failing auth method.
  curl --insecure --silent "https://${LXD_ADDR}/1.0" \
    | jq --exit-status '.metadata.auth == "untrusted"'

  sleep 3
  kill_go_proc "${mon_pid}" || true
  jq --exit-status --slurp \
    'map(select(.type == "security" and (.metadata.name // "" | startswith("authn_login_fail")))) | length == 0' \
    "${monfile}"
  rm -f "${monfile}"

  sub_test "Verify authn_login_fail:tls fires when an untrusted client cert is presented"
  gen_cert_and_key "authn-untrusted-cert"

  monfile="${TEST_DIR}/authn-login-fail-tls.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  mon_pid=$!
  sleep 0.2

  curl --insecure --silent \
    --cert "${LXD_CONF}/authn-untrusted-cert.crt" \
    --key "${LXD_CONF}/authn-untrusted-cert.key" \
    "https://${LXD_ADDR}/1.0/instances" \
    | jq --exit-status '.error_code == 403'

  _wait_authn_event "${monfile}" "${mon_pid}" \
    'map(select(.type == "security" and .metadata.name == "authn_login_fail:tls" and .metadata.level == "warning" and (.metadata.requestor.address // "") != "")) | length >= 1'

  sub_test "Verify authn_token_created fires when a bearer token is issued"
  lxc auth identity create bearer/authn-bearer-test
  local bearer_id
  bearer_id="$(lxc auth identity list bearer --format json | jq --raw-output '.[] | select(.name == "authn-bearer-test") | .id')"

  monfile="${TEST_DIR}/authn-token-created.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  mon_pid=$!
  sleep 0.2

  local issued_token
  issued_token="$(lxc auth identity token issue bearer/authn-bearer-test --quiet)"
  [ -n "${issued_token}" ]

  _wait_authn_event "${monfile}" "${mon_pid}" \
    "map(select(.type == \"security\" and .metadata.name == \"authn_token_created:${bearer_id}\" and .metadata.level == \"info\")) | length >= 1"

  sub_test "Verify authn_token_revoked fires when a bearer token is revoked"
  monfile="${TEST_DIR}/authn-token-revoked.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  mon_pid=$!
  sleep 0.2

  lxc auth identity token revoke bearer/authn-bearer-test

  _wait_authn_event "${monfile}" "${mon_pid}" \
    "map(select(.type == \"security\" and .metadata.name == \"authn_token_revoked:${bearer_id}\" and .metadata.level == \"info\")) | length >= 1"

  lxc auth identity delete bearer/authn-bearer-test

  sub_test "Verify authn_certificate_change fires when TLS certificate material is replaced (admin)"
  gen_cert_and_key "authn-orig-cert"
  lxc config trust add "${LXD_CONF}/authn-orig-cert.crt" --name authn-test-cert
  local old_fp
  old_fp="$(cert_fingerprint "${LXD_CONF}/authn-orig-cert.crt")"
  gen_cert_and_key "authn-new-cert"

  monfile="${TEST_DIR}/authn-cert-change.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  mon_pid=$!
  sleep 0.2

  lxc query --request PUT \
    --data "$(jq --null-input \
      --argjson cert "$(cert_to_json "${LXD_CONF}/authn-new-cert.crt")" \
      --arg name "authn-test-cert" \
      '{certificate: $cert, name: $name, restricted: false, projects: [], type: "client"}')" \
    "/1.0/certificates/${old_fp}"

  _wait_authn_event "${monfile}" "${mon_pid}" \
    "map(select(.type == \"security\" and .metadata.name == \"authn_certificate_change:${old_fp}\" and .metadata.level == \"info\")) | length >= 1"

  local new_fp
  new_fp="$(cert_fingerprint "${LXD_CONF}/authn-new-cert.crt")"
  lxc config trust remove "${new_fp}"

  sub_test "Verify authn_certificate_change fires on self-update (old fingerprint in event name)"
  gen_cert_and_key "authn-self-cert"
  lxc config trust add "${LXD_CONF}/authn-self-cert.crt" --name authn-self-cert
  local self_old_fp
  self_old_fp="$(cert_fingerprint "${LXD_CONF}/authn-self-cert.crt")"

  # Mark as restricted so the PUT goes through doCertificateUpdateUnprivileged.
  lxc config trust show "${self_old_fp}" \
    | sed "s/restricted: false/restricted: true/" \
    | lxc config trust edit "${self_old_fp}"

  gen_cert_and_key "authn-self-new-cert"

  monfile="${TEST_DIR}/authn-cert-selfchange.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  mon_pid=$!
  sleep 0.2

  CERTNAME="authn-self-cert" my_curl --request PUT \
    --data "$(jq --null-input \
      --argjson cert "$(cert_to_json "${LXD_CONF}/authn-self-new-cert.crt")" \
      --arg name "authn-self-cert" \
      '{certificate: $cert, name: $name, restricted: true, projects: [], type: "client"}')" \
    "https://${LXD_ADDR}/1.0/certificates/${self_old_fp}"

  # Caller authenticated with the old cert, so the requestor username equals
  # the old fingerprint.
  _wait_authn_event "${monfile}" "${mon_pid}" \
    "map(select(.type == \"security\" and .metadata.name == \"authn_certificate_change:${self_old_fp}\" and .metadata.requestor.username == \"${self_old_fp}\")) | length >= 1"

  local self_new_fp
  self_new_fp="$(cert_fingerprint "${LXD_CONF}/authn-self-new-cert.crt")"
  lxc config trust remove "${self_new_fp}"
}
