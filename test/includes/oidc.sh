# mini-oidc related test helpers.

spawn_oidc() {
  # Return if OIDC is already set up.
  [ -e "${TEST_DIR}/oidc.pid" ] && return

  (
    cd mini-oidc || return
    # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
    go build -v -buildvcs=false ./

    PORT="$(local_tcp_port)"
    echo "${PORT}" > "${TEST_DIR}/oidc.port"
    ./mini-oidc "${PORT}" "${TEST_DIR}/oidc.user" &
    echo $! > "${TEST_DIR}/oidc.pid"

    sleep 3
  )
}

kill_oidc() {
  [ ! -e "${TEST_DIR}/oidc.pid" ] && return

  kill -9 "$(< "${TEST_DIR}/oidc.pid")"
  rm -f "${TEST_DIR}/oidc.pid"
  rm -f "${TEST_DIR}/oidc.port"
  rm -f "${TEST_DIR}/oidc.user"
}

set_oidc() {
  echo "${1}" > "${TEST_DIR}/oidc.user"
  echo "${2:-}" >> "${TEST_DIR}/oidc.user"
}
