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

# wait_for_security_event slurps the given JSON-Lines monitor file and waits
# up to 10 seconds for at least one event matching the provided jq condition.
# Usage: wait_for_security_event <monfile> <jq_select_body>
wait_for_security_event() {
  local monfile="${1}"
  local jq_select="${2}"

  for _ in $(seq 10); do
    if jq --exit-status --slurp \
      'map(select(.type == "security")) | map(select('"${jq_select}"')) | length >= 1' \
      "${monfile}"; then
      return 0
    fi
    sleep 1
  done

  echo "Expected security event not found in ${monfile}: ${jq_select}"
  cat "${monfile}"
  return 1
}

# count_events_tolerant counts JSON-Lines events in monfile matching the given
# jq select expression, tolerating partial or garbled lines that can appear
# around a monitor restart. Echoes the count.
# Usage: count_events_tolerant <monfile> <jq_select_body>
count_events_tolerant() {
  local monfile="${1}"
  local jq_select="${2}"

  jq --slurp --raw-input '
    split("\n")
    | map(fromjson? | select(. != null))
    | map(select('"${jq_select}"'))
    | length' "${monfile}"
}

# assert_request_fields validates that a single security event (selected via
# the provided jq filter) carries the request-scoped fields the emitter is
# expected to populate. The OWASP-named fields (host_ip, hostname, port, …)
# are produced only at the Loki forwarder; the monitor stream exposes the
# leaner Go-style EventSecurity struct.
# Usage: assert_request_fields <monfile> <jq_select_body>
assert_request_fields() {
  local monfile="${1}"
  local jq_select="${2}"

  jq --exit-status --slurp \
    'map(select(.type == "security")) | map(select('"${jq_select}"'))
     | .[0] as $e
     | ($e.metadata.requestor.address // "") != ""
       and ($e.metadata.requestor.username // "") != ""
       and ($e.metadata.requestor.protocol // "") != ""
       and ($e.metadata.request_method // "") != ""
       and ($e.metadata.request_path // "") != ""' \
    "${monfile}"
}

test_security_authz_events() {
  local monfile="${TEST_DIR}/authz-events.jsonl"
  rm -f "${monfile}"

  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!

  # Wait until the monitor connection is live.
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done
  kill -0 "${mon_pid}"

  sub_test "authz_admin: group_create fires on auth group create"
  lxc auth group create authz-evt-g1
  wait_for_security_event "${monfile}" '.metadata.name == "authz_admin:group_create:authz-evt-g1"'
  assert_request_fields "${monfile}" '.metadata.name == "authz_admin:group_create:authz-evt-g1"'

  sub_test "authz_admin: group_edit fires on auth group edit"
  printf 'description: updated\npermissions: []\n' | lxc auth group edit authz-evt-g1
  wait_for_security_event "${monfile}" '.metadata.name == "authz_admin:group_edit:authz-evt-g1"'

  sub_test "authz_admin: group_edit fires on auth group rename"
  lxc auth group rename authz-evt-g1 authz-evt-g2
  wait_for_security_event "${monfile}" '.metadata.name == "authz_admin:group_edit:authz-evt-g2"'

  sub_test "authz_admin: group_delete fires on auth group delete"
  lxc auth group delete authz-evt-g2
  wait_for_security_event "${monfile}" '.metadata.name == "authz_admin:group_delete:authz-evt-g2"'

  sub_test "authz_admin: idp_group_create fires on identity-provider-group create"
  lxc auth identity-provider-group create authz-evt-idp1
  wait_for_security_event "${monfile}" '.metadata.name == "authz_admin:idp_group_create:authz-evt-idp1"'
  assert_request_fields "${monfile}" '.metadata.name == "authz_admin:idp_group_create:authz-evt-idp1"'

  sub_test "authz_admin: idp_group_edit fires on identity-provider-group rename"
  lxc auth identity-provider-group rename authz-evt-idp1 authz-evt-idp2
  wait_for_security_event "${monfile}" '.metadata.name == "authz_admin:idp_group_edit:authz-evt-idp2"'

  sub_test "authz_admin: idp_group_delete fires on identity-provider-group delete"
  lxc auth identity-provider-group delete authz-evt-idp2
  wait_for_security_event "${monfile}" '.metadata.name == "authz_admin:idp_group_delete:authz-evt-idp2"'

  sub_test "authz_admin: identity_create fires on TLS identity create"
  lxc auth identity create tls/authz-evt-user1 --quiet
  wait_for_security_event "${monfile}" '.metadata.name | startswith("authz_admin:identity_create:tls/")'

  local tls_pending_id
  tls_pending_id="$(lxc auth identity list --format csv | awk -F, '/^tls,.*authz-evt-user1/ {print $4}')"

  sub_test "authz_admin: identity_delete fires on TLS identity delete"
  lxc auth identity delete "tls/${tls_pending_id}"
  wait_for_security_event "${monfile}" '.metadata.name == ("authz_admin:identity_delete:tls/'"${tls_pending_id}"'")'

  sub_test "authz_admin: identity_edit fires on bearer identity group membership change"
  lxc auth group create authz-evt-g3
  lxc auth identity create bearer/authz-evt-bearer1
  local bearer_id
  bearer_id="$(lxc auth identity show bearer/authz-evt-bearer1 | awk '/^id:/ {print $2}')"
  lxc auth identity group add "bearer/${bearer_id}" authz-evt-g3
  wait_for_security_event "${monfile}" '.metadata.name == ("authz_admin:identity_edit:bearer/'"${bearer_id}"'")'
  lxc auth identity delete "bearer/${bearer_id}"
  lxc auth group delete authz-evt-g3

  sub_test "authz_fail fires when fine-grained identity attempts denied edit"
  # Create a TLS user with only can_view_projects on the server. They should be denied
  # when trying to edit a project.
  lxc auth group create authz-evt-viewer
  lxc auth group permission add authz-evt-viewer server can_view_projects
  local authz_conf token
  authz_conf="$(mktemp -d -p "${TEST_DIR}" XXX)"
  LXD_CONF="${authz_conf}" gen_cert_and_key "client"
  token="$(lxc auth identity create tls/authz-evt-viewer-user --quiet --group authz-evt-viewer)"
  LXD_CONF="${authz_conf}" lxc remote add authz-evt "${token}"
  # Attempt an edit that requires can_edit on the default project. Denied.
  ! LXD_CONF="${authz_conf}" lxc_remote project set authz-evt:default user.foo=bar || false
  wait_for_security_event "${monfile}" '.metadata.name == "authz_fail:can_edit:/1.0/projects/default"'
  assert_request_fields "${monfile}" '.metadata.name == "authz_fail:can_edit:/1.0/projects/default"'

  sub_test "authz_fail does NOT fire on list filtering (GetPermissionChecker)"
  # GetPermissionChecker is only used to filter project entities in the list
  # handler; any emission from it would carry a /1.0/projects/<name> URL.
  # Direct CheckPermission denials on other entities are expected and
  # must be ignored here.
  local list_filter_authz_fail_jq='map(select(.type == "security" and (.metadata.name | test("^authz_fail:[^:]+:/1\\.0/projects/")))) | length'
  local list_filter_authz_fail_before list_filter_authz_fail_after
  list_filter_authz_fail_before="$(jq --slurp "${list_filter_authz_fail_jq}" "${monfile}")"
  local _project_list
  _project_list="$(LXD_CONF="${authz_conf}" lxc_remote project list authz-evt: --format csv)"
  _project_list="$(LXD_CONF="${authz_conf}" lxc_remote project list authz-evt: --format csv)"
  sleep 1
  list_filter_authz_fail_after="$(jq --slurp "${list_filter_authz_fail_jq}" "${monfile}")"
  [ "$((list_filter_authz_fail_after - list_filter_authz_fail_before))" = "0" ]

  sub_test "authz_fail does NOT fire on can_edit display probes (server/storage_pool)"
  # GET /1.0 and GET /1.0/storage-pools/<name> probe can_edit on server and
  # storage_pool to decide whether to render sensitive config. Those probes
  # are expected denials, not real authorization failures, and must not
  # produce authz_fail events.
  local probe_jq='map(select(.type == "security" and (.metadata.name == "authz_fail:can_edit:/1.0" or (.metadata.name | test("^authz_fail:can_edit:/1\\.0/storage-pools/"))))) | length'
  local probe_before probe_after _probe_info _probe_pool _probe_show
  probe_before="$(jq --slurp "${probe_jq}" "${monfile}")"
  _probe_info="$(LXD_CONF="${authz_conf}" lxc_remote info authz-evt:)"
  _probe_pool="$(lxc storage list --format csv | awk --field-separator=, 'NR==1 {print $1}')"
  if [ -n "${_probe_pool}" ]; then
    _probe_show="$(LXD_CONF="${authz_conf}" lxc_remote storage show "authz-evt:${_probe_pool}")"
  fi
  sleep 1
  probe_after="$(jq --slurp "${probe_jq}" "${monfile}")"
  [ "$((probe_after - probe_before))" = "0" ]

  sub_test "Lifecycle and authz_admin coexist without duplication"
  # Restart monitor capturing both types. Wait for the old process to fully die
  # before redirecting, to avoid a partial buffered write corrupting the file.
  local old_mon_pid="${mon_pid}"
  kill_go_proc "${old_mon_pid}" || true
  wait "${old_mon_pid}" || true
  lxc monitor --type=security --type=lifecycle --format=json > "${monfile}" &
  mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done

  local coexist_lifecycle_jq='.type == "lifecycle" and .metadata.action == "auth-group-created" and .metadata.source == "/1.0/auth/groups/authz-evt-coexist"'
  local coexist_security_jq='.type == "security" and .metadata.name == "authz_admin:group_create:authz-evt-coexist"'

  lxc auth group create authz-evt-coexist
  # Exactly one lifecycle and one authz_admin event for the create.
  for _ in $(seq 10); do
    if [ "$(count_events_tolerant "${monfile}" "${coexist_lifecycle_jq}")" = "1" ] \
      && [ "$(count_events_tolerant "${monfile}" "${coexist_security_jq}")" = "1" ]; then
      break
    fi
    sleep 1
  done
  [ "$(count_events_tolerant "${monfile}" "${coexist_lifecycle_jq}")" = "1" ]
  [ "$(count_events_tolerant "${monfile}" "${coexist_security_jq}")" = "1" ]

  # Cleanup.
  lxc auth group delete authz-evt-coexist
  LXD_CONF="${authz_conf}" lxc remote remove authz-evt
  local viewer_fp
  viewer_fp="$(lxc auth identity list --format csv | awk -F, '/^tls,.*,authz-evt-viewer/ {print $4}')"
  lxc auth identity delete "tls/${viewer_fp}"
  lxc auth group delete authz-evt-viewer

  kill_go_proc "${mon_pid}" || true
  rm -f "${monfile}"
}
