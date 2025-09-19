test_concurrent() {
  ensure_import_testimage

  spawn_container() {
    set -e

    name=concurrent-${1}

    lxc launch testimage "${name}"
    # XXX: `[ "$(lxc list -f csv -c s "${name}")" = "RUNNING" ]` is too fast and leads to races with exec/delete
    lxc list --fast "${name}" | grep -wF RUNNING
    [ "$(echo abc | lxc exec "${name}" -- cat)" = "abc" ]
    lxc delete --force "${name}"
  }

  PIDS=""

  # spawn x times number of available CPUs
  COUNT="$(($(nproc)*8))"
  for id in $(seq "${COUNT}"); do
    spawn_container "${id}" 2>&1 | tee "${LXD_DIR}/lxc-${id}.out" &
    PIDS="${PIDS} $!"
  done

  for pid in ${PIDS}; do
    # ignore PIDs that vanished (wait returns 127 for those)
    wait "${pid}" || [ "$?" -eq 127 ]
  done

  # Verify no `concurrent-` instances was left behind
  [ "$(lxc list -f csv -c n concurrent- || echo fail)" = "" ]
}
