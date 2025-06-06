test_concurrent() {
  ensure_import_testimage

  spawn_container() {
    set -e

    name=concurrent-${1}

    lxc launch testimage "${name}"
    lxc info "${name}" | grep -wF RUNNING
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
    wait "${pid}"
  done

  ! lxc list | grep -F concurrent || false
}
