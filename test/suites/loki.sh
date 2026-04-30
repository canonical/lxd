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
  ! lxc config set loki.types="lifecycle,logging,invalid_type" || false

  sub_test "Verify lifecycle events still route to Loki when security is included in loki.types"
  ensure_import_testimage
  lxc launch testimage c-loki-security
  lxc delete -f c-loki-security

  sub_test "Verify sys_monitor_disabled fires when Loki is disabled after being enabled"
  local monfile="${TEST_DIR}/loki-monitor-disabled.jsonl"
  lxc monitor --type=security --format=json > "${monfile}" &
  local mon_pid=$!
  for _ in $(seq 10); do
    kill -0 "${mon_pid}" && break
    sleep 1
  done
  kill -0 "${mon_pid}"

  # Disabling loki.api.url tears down the loki client; this is the
  # enabled -> disabled transition that emits sys_monitor_disabled.
  lxc config set loki.api.url="" loki.auth.username="" loki.auth.password="" loki.types=""

  for _ in $(seq 10); do
    jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | length >= 1' "${monfile}" && break
    sleep 1
  done

  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | length == 1' "${monfile}"
  # Loss of monitoring is a warning-level daemon-level event with no requestor.
  jq --exit-status --slurp 'map(select(.type == "security" and .metadata.name == "sys_monitor_disabled")) | .[0] | (.metadata.level == "warning") and (.metadata.description == "Loki monitoring disabled") and (.metadata | has("requestor") | not) and (.metadata | has("request_method") | not)' "${monfile}"

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
