# mini-oidc related test helpers.

spawn_oidc() {
  # Return if OIDC is already set up.
  [ -e "${TEST_DIR}/oidc.pid" ] && return

  PORT="$(local_tcp_port)"
  echo "${PORT}" > "${TEST_DIR}/oidc.port"
  mini-oidc "${PORT}" "${TEST_DIR}/oidc.user" &
  echo $! > "${TEST_DIR}/oidc.pid"

  sleep 3
}

kill_oidc() {
  [ ! -e "${TEST_DIR}/oidc.pid" ] && return

  kill -9 "$(< "${TEST_DIR}/oidc.pid")"
  rm -f "${TEST_DIR}/oidc.pid"
  rm -f "${TEST_DIR}/oidc.port"
  rm -f "${TEST_DIR}/oidc.user"
}

set_oidc() {
  echo "${1}
${2:-}" > "${TEST_DIR}/oidc.user"
}
