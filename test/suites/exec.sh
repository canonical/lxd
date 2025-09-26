exec_container_noninteractive() {
    [ "$(echo "abc${1}" | lxc exec x1 --force-noninteractive -- cat)" = "abc${1}" ]
}

exec_container_interactive() {
    [ "$(echo "abc${1}" | lxc exec x1 -- cat)" = "abc${1}" ]
}

test_exec() {
  ensure_import_testimage

  lxc launch testimage x1
  [ "$(lxc list -f csv -c s x1)" = "RUNNING" ]

  for i in $(seq 1 25); do
    exec_container_interactive "${i}" > "${LXD_DIR}/exec-${i}.out" 2>&1
  done

  for i in $(seq 1 25); do
    exec_container_noninteractive "${i}" > "${LXD_DIR}/exec-${i}.out" 2>&1
  done

  # Check non-websocket based exec works.
  opID="$(lxc query -X POST -d '{"command":["touch","/root/foo1"],"record-output":false}' /1.0/instances/x1/exec | jq -er .id)"
  sleep 0.1
  lxc query  "/1.0/operations/${opID}" | jq -e '.metadata.return == 0'
  lxc exec x1 -- stat /root/foo1

  opID="$(lxc query -X POST -d '{"command":["missingcmd"],"record-output":false}' /1.0/instances/x1/exec | jq -er .id)"
  sleep 0.1
  lxc query "/1.0/operations/${opID}" | jq -e '.metadata.return == 127'

  echo "hello" | lxc exec x1 -- tee /root/foo1
  opID="$(lxc query -X POST -d '{"command":["cat","/root/foo1"],"record-output":true}' /1.0/instances/x1/exec | jq -er .id)"
  sleep 0.1
  stdOutURL="$(lxc query "/1.0/operations/${opID}" | jq -er '.metadata.output["1"]')"
  [ "$(lxc query "${stdOutURL}")" = "hello" ]

  lxc delete --force x1
}

test_concurrent_exec() {
  ensure_import_testimage

  lxc launch testimage x1
  [ "$(lxc list -f csv -c s x1)" = "RUNNING" ]

  PIDS=""
  for i in $(seq 1 25); do
    exec_container_interactive "${i}" > "${LXD_DIR}/exec-${i}.out" 2>&1 &
    PIDS="${PIDS} $!"
  done

  for i in $(seq 1 25); do
    exec_container_noninteractive "${i}" > "${LXD_DIR}/exec-${i}.out" 2>&1 &
    PIDS="${PIDS} $!"
  done

  for pid in ${PIDS}; do
    wait "${pid}"
  done

  lxc delete --force x1
}

test_exec_exit_code() {
  ensure_import_testimage
  lxc launch testimage x1

  lxc exec x1 -- true || exitCode=$?
  [ "${exitCode:-0}" -eq 0 ]

  lxc exec x1 -- false || exitCode=$?
  [ "${exitCode:-0}" -eq 1 ]

  lxc exec x1 -- invalid-command || exitCode=$?
  [ "${exitCode:-0}" -eq 127 ]

  # Signaling the process spawned by lxc exec and checking its exit code.
  # Simulates what can happen if the container stops in the middle of lxc exec.
  (sleep 1 && lxc exec x1 -- killall -s SIGTERM sleep) &
  lxc exec x1 -- sleep 60 || exitCode=$?
  [ "${exitCode:-0}" -eq 143 ] # 128 + 15(SIGTERM)

  (sleep 1 && lxc exec x1 -- killall -s SIGHUP sleep) &
  lxc exec x1 -- sleep 60 || exitCode=$?
  [ "${exitCode:-0}" -eq 129 ] # 128 + 1(SIGHUP)

  (sleep 1 && lxc exec x1 -- killall -s SIGKILL sleep) &
  lxc exec x1 -- sleep 60 || exitCode=$?
  [ "${exitCode:-0}" -eq 137 ] # 128 + 9(SIGKILL)

  # Try disconnecting a container stopping forcefully.
  (sleep 1 && lxc stop -f x1) &
  lxc exec x1 -- sleep 60 || exitCode=$?
  [ "${exitCode:-0}" -eq 137 ]
  wait $!

  lxc delete --force x1
}
