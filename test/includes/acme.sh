# mini-acme related test helpers.

# spawn_acme [<validation-addr>]
# Start mini-acme with optional HTTP-01 validation against the given address.
spawn_acme() {
  # Return if ACME is already set up.
  [ -e "${TEST_DIR}/acme.pid" ] && return

  local port
  port="$(local_tcp_port)"
  echo "${port}" > "${TEST_DIR}/acme.port"

  mini-acme "${port}" "${TEST_DIR}/mini-acme-ca.crt" "${1:-}" &
  echo $! > "${TEST_DIR}/acme.pid"

  # Wait for the server to be ready.
  for _ in $(seq 3); do
    curl -s --cacert "${TEST_DIR}/mini-acme-ca.crt" -o /dev/null "https://127.0.0.1:${port}/directory" 2>/dev/null && break
    sleep 0.1
  done
}

kill_acme() {
  [ ! -e "${TEST_DIR}/acme.pid" ] && return

  kill_go_proc "$(< "${TEST_DIR}/acme.pid")"
  rm -f "${TEST_DIR}/acme.pid"
  rm -f "${TEST_DIR}/acme.port"
  rm -f "${TEST_DIR}/mini-acme-ca.crt"
}
