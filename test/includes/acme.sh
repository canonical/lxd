# mini-acme related test helpers.

# spawn_acme [<validation-addr> [<listen-addr>]]
# Start mini-acme with optional HTTP-01 validation against the given address.
spawn_acme() {
  # Return if ACME is already set up.
  [ -e "${TEST_DIR}/acme.pid" ] && return

  local validation_addr="${1:-}"
  local listen_addr="${2:-127.0.0.1}"

  local port
  port="$(local_tcp_port)"
  echo "${port}" > "${TEST_DIR}/acme.port"

  local addr="${listen_addr}:${port}"

  mini-acme "${addr}" "${TEST_DIR}/mini-acme-ca.crt" "${validation_addr}" &
  echo $! > "${TEST_DIR}/acme.pid"

  # Wait for the server to be ready.
  success=0
  for _ in $(seq 10); do
    if curl -s --cacert "${TEST_DIR}/mini-acme-ca.crt" -o /dev/null "https://${addr}/directory" 2>/dev/null; then
      success=1
      break
    fi

    sleep 0.1
  done

  if [ "${success}" = 0 ]; then
    echo "mini-acme: Not available within 1 second"
    false
  fi
}

kill_acme() {
  [ ! -e "${TEST_DIR}/acme.pid" ] && return

  kill -9 "$(< "${TEST_DIR}/acme.pid")"
  rm -f "${TEST_DIR}/acme.pid"
  rm -f "${TEST_DIR}/acme.port"
  rm -f "${TEST_DIR}/mini-acme-ca.crt"
}
