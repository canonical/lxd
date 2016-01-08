#!/bin/sh

test_concurrent() {
  ensure_import_testimage

  spawn_container() {
    set -e

    name=concurrent-${1}

    lxc launch testimage "${name}"
    lxc info "${name}" | grep Running
    echo abc | lxc exec "${name}" -- cat | grep abc
    lxc stop "${name}" --force
    lxc delete "${name}"
  }

  PIDS=""

  for id in $(seq $(($(find /sys/bus/cpu/devices/ -type l | wc -l)*8))); do
    spawn_container "${id}" 2>&1 | tee "${LXD_DIR}/lxc-${id}.out" &
    PIDS="${PIDS} $!"
  done

  for pid in ${PIDS}; do
    wait "${pid}"
  done

  ! lxc list | grep -q concurrent
}
