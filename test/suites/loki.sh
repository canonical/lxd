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
