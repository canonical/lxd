# mini-oidc related test helpers.

spawn_oidc() {
  local port=${1:-}

  # Return if OIDC is already set up.
  [ -e "${TEST_DIR}/oidc.pid" ] && return

  if [ "${port}" = "" ]; then
    port="$(local_tcp_port)"
  fi

  echo "${port}" > "${TEST_DIR}/oidc.port"
  mini-oidc "${port}" "${TEST_DIR}/oidc.user" &
  echo $! > "${TEST_DIR}/oidc.pid"

  sleep 3
}

kill_oidc() {
  [ ! -e "${TEST_DIR}/oidc.pid" ] && return

  kill_go_proc "$(< "${TEST_DIR}/oidc.pid")"
  rm -f "${TEST_DIR}/oidc.pid"
  rm -f "${TEST_DIR}/oidc.port"
  rm -f "${TEST_DIR}/oidc.user"
}

set_oidc() {
  echo "${1}
${2:-}" > "${TEST_DIR}/oidc.user"
}
