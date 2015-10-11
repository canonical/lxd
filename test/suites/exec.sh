#!/bin/sh

test_concurrent_exec() {
  ensure_import_testimage

  name=x1
  lxc launch testimage x1
  lxc list ${name} | grep RUNNING

  exec_container() {
    echo "abc${1}" | lxc exec "${name}" -- cat | grep abc
  }

  PIDS=""
  for i in $(seq 1 50); do
    exec_container "${i}" > "${LXD_DIR}/exec-${i}.out" 2>&1 &
    PIDS="${PIDS} $!"
  done

  for pid in ${PIDS}; do
    wait "${pid}"
  done

  lxc stop "${name}" --force
  lxc delete "${name}"
}
