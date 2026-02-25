# loki related test helpers.

spawn_loki() {
  # Return if loki is already set up.
  [ -e "${TEST_DIR}/loki.pid" ] && return

  mini-loki "${TEST_DIR}" &
  echo $! > "${TEST_DIR}/loki.pid"
  sleep 0.1
}

kill_loki() {
  [ ! -e "${TEST_DIR}/loki.pid" ] && return

  kill_go_proc "$(< "${TEST_DIR}/loki.pid")"
  rm "${TEST_DIR}/loki.pid"
}
