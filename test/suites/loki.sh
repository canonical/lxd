test_loki() {
  local log_file="${TEST_DIR}/loki.logs"
  spawn_loki

  lxc config set loki.api.url="http://127.0.0.1:3100" loki.auth.username="loki" loki.auth.password="pass"
  lxc config set loki.labels="env=prod,app=web" loki.types="lifecycle,logging,ovn"

  ensure_import_testimage
  lxc launch testimage c1
  lxc restart -f c1
  lxc delete -f c1

  lxc init --empty c2
  lxc delete c2

  # Changing the loki configuration sends any accumulated logs to the test server
  lxc config set loki.api.url="" loki.auth.username="" loki.auth.password="" loki.labels="" loki.types=""

  # Check there are both logging and lifecycle entries
  jq --exit-status '.streams[].stream | select(.type == "logging")' "${log_file}"
  jq --exit-status '.streams[].stream | select(.type == "lifecycle")' "${log_file}"

  # Check the expected lifecycle events for c1
  jq --exit-status '.streams[] | select(.stream.name == "c1") | .values[][1]' "${log_file}"  # debug
  jq --exit-status '.streams[] | select(.stream.name == "c1") | .values[][1] | select(contains("action=\"instance-created\""))' "${log_file}"
  jq --exit-status '.streams[] | select(.stream.name == "c1") | .values[][1] | select(contains("action=\"instance-started\""))' "${log_file}"
  jq --exit-status '.streams[] | select(.stream.name == "c1") | .values[][1] | select(contains("action=\"instance-restarted\""))' "${log_file}"
  jq --exit-status '.streams[] | select(.stream.name == "c1") | .values[][1] | select(contains("action=\"instance-stopped\""))' "${log_file}"
  jq --exit-status '.streams[] | select(.stream.name == "c1") | .values[][1] | select(contains("action=\"instance-deleted\""))' "${log_file}"

  # Check the expected lifecycle events for c2
  jq --exit-status '.streams[] | select(.stream.name == "c2") | .values[][1]' "${log_file}"  # debug
  jq --exit-status '.streams[] | select(.stream.name == "c2") | .values[][1] | select(contains("action=\"instance-created\""))' "${log_file}"
  jq --exit-status '.streams[] | select(.stream.name == "c2") | .values[][1] | select(contains("action=\"instance-deleted\""))' "${log_file}"

  # Cleanup
  kill_loki
  rm "${log_file}"
}

test_loki_security_types() {
  local log_file="${TEST_DIR}/loki.logs"
  spawn_loki

  sub_test "Verify security is accepted as a valid loki.types value"
  lxc config set loki.api.url="http://127.0.0.1:3100" loki.auth.username="loki" loki.auth.password="pass"
  lxc config set loki.types="lifecycle,logging,security"
  [ "$(lxc config get loki.types)" = "lifecycle,logging,security" ]

  # An unknown event type must be rejected by the validator.
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" lxc config set loki.types="invalid_type" 2>&1)" = 'Error: Cannot set "loki.types" to "invalid_type": Item "invalid_type": Invalid value "invalid_type" (not one of [lifecycle logging ovn security])' ]

  sub_test "Verify lifecycle events still route to Loki when security is included in loki.types"
  ensure_import_testimage
  lxc launch testimage c-loki-security
  lxc delete -f c-loki-security

  sub_test "Verify sys_monitor_disabled does not fire when security is removed from loki.types"
  local monfile="${TEST_DIR}/loki-monitor-disabled.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done
  kill -0 "${mon_pid}"

  # The Loki client stays up; sys_monitor_disabled is reserved for the
  # full enabled -> disabled transition.
  lxc config set loki.types="lifecycle,logging"
  sleep 1
  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | length == 0' "${monfile}"

  sub_test "Verify sys_monitor_disabled fires when Loki is disabled after being enabled"
  local sys_monitor_disabled_count_before_full_off sys_monitor_disabled_count_after_full_off
  sys_monitor_disabled_count_before_full_off=$(jq --raw-output --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | length' "${monfile}")

  # Disabling loki.api.url tears down the loki client; this is the
  # enabled -> disabled transition that emits sys_monitor_disabled.
  lxc config set loki.api.url="" loki.auth.username="" loki.auth.password="" loki.types=""

  for _ in $(seq 10); do
    sys_monitor_disabled_count_after_full_off=$(jq --raw-output --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | length' "${monfile}")
    [ "${sys_monitor_disabled_count_after_full_off}" -gt "${sys_monitor_disabled_count_before_full_off}" ] && break
    sleep 1
  done

  [ "${sys_monitor_disabled_count_after_full_off}" -gt "${sys_monitor_disabled_count_before_full_off}" ]
  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled" and .metadata.description == "Loki monitoring disabled")) | length == 1' "${monfile}"

  sub_test "Verify sys_monitor_disabled does not fire when Loki was never configured"
  # Loki is already off; toggling an unrelated server config must not raise
  # a fresh sys_monitor_disabled event.
  local sys_monitor_disabled_count_before
  sys_monitor_disabled_count_before=$(jq --raw-output --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | length' "${monfile}")
  lxc config set core.proxy_ignore_hosts="example.invalid"
  lxc config unset core.proxy_ignore_hosts
  sleep 1
  local sys_monitor_disabled_count_after
  sys_monitor_disabled_count_after=$(jq --raw-output --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | length' "${monfile}")
  [ "${sys_monitor_disabled_count_after}" = "${sys_monitor_disabled_count_before}" ]

  kill_go_proc "${mon_pid}" || true
  rm "${monfile}"

  jq --exit-status '.streams[].stream | select(.type == "lifecycle")' "${log_file}"

  # Cleanup.
  kill_loki
  rm "${log_file}"
}

test_loki_security_forwarding() {
  local log_file="${TEST_DIR}/loki.logs"
  spawn_loki

  sub_test "Verify security events forward to Loki with OWASP serialization"
  lxc config set loki.api.url="http://127.0.0.1:3100" loki.auth.username="loki" loki.auth.password="pass"
  lxc config set loki.types="security"

  # Trigger authn_login_fail:tls by presenting an untrusted client cert to an
  # authenticated endpoint (mirrors test_authn_events in security.sh).
  gen_cert_and_key "loki-untrusted-cert"
  curl --insecure --silent \
    --cert "${LXD_CONF}/loki-untrusted-cert.crt" \
    --key "${LXD_CONF}/loki-untrusted-cert.key" \
    "https://${LXD_ADDR}/1.0/instances" \
    | jq --exit-status '.error_code == 403'

  # Changing the loki configuration sends any accumulated logs to the test server.
  lxc config set loki.api.url="" loki.auth.username="" loki.auth.password="" loki.types=""

  # The security stream must exist and every line must carry OWASP fields.
  jq --exit-status '.streams[].stream | select(.type == "security")' "${log_file}"
  # Assert OWASP fields that securityEventToOWASP populates unconditionally.
  # user_id, useragent and source_ip are only set when the requestor has the
  # corresponding metadata, which is not the case for authn_login_fail:tls.
  jq --exit-status 'all(
    .streams[] | select(.stream.type == "security") | .values[][1];
    fromjson | (.appid == "lxd" and .type == "security" and (.event | startswith("authn_login_fail")) and has("cluster_identifier") and has("datetime") and has("event_source"))
  )' "${log_file}"

  # mini-loki holds loki.logs open for the process lifetime, so a plain rm
  # leaves the daemon writing to an unlinked inode. Restart it to get a fresh
  # file at the same path for the second sub-test.
  kill_loki
  rm "${log_file}"
  spawn_loki

  sub_test "Verify security events are filtered when not in loki.types"
  lxc config set loki.api.url="http://127.0.0.1:3100" loki.auth.username="loki" loki.auth.password="pass"
  lxc config set loki.types="lifecycle"

  ensure_import_testimage
  lxc launch testimage c-no-security-events
  lxc delete -f c-no-security-events

  # Flush logs.
  lxc config set loki.api.url="" loki.auth.username="" loki.auth.password="" loki.types=""

  # Lifecycle events must be present (proves forwarding is alive); security
  # events must be absent (proves the type filter works).
  jq --exit-status '.streams[].stream | select(.type == "lifecycle")' "${log_file}"
  ! jq --exit-status '.streams[].stream | select(.type == "security")' "${log_file}" || false

  # Cleanup.
  kill_loki
  rm "${log_file}"
}
