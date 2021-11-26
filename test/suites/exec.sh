test_exec() {
  ensure_import_testimage

  name=x1
  lxc launch testimage x1
  lxc list ${name} | grep RUNNING

  exec_container_noninteractive() {
    echo "abc${1}" | lxc exec "${name}" --force-noninteractive -- cat | grep abc
  }

  exec_container_interactive() {
    echo "abc${1}" | lxc exec "${name}" -- cat | grep abc
  }

  for i in $(seq 1 25); do
    exec_container_interactive "${i}" > "${LXD_DIR}/exec-${i}.out" 2>&1
  done

  for i in $(seq 1 25); do
    exec_container_noninteractive "${i}" > "${LXD_DIR}/exec-${i}.out" 2>&1
  done

  lxc stop "${name}" --force
  lxc delete "${name}"
}

test_concurrent_exec() {
  if [ -z "${LXD_CONCURRENT:-}" ]; then
    echo "==> SKIP: LXD_CONCURRENT isn't set"
    return
  fi

  ensure_import_testimage

  name=x1
  lxc launch testimage x1
  lxc list ${name} | grep RUNNING

  exec_container_noninteractive() {
    echo "abc${1}" | lxc exec "${name}" --force-noninteractive -- cat | grep abc
  }

  exec_container_interactive() {
    echo "abc${1}" | lxc exec "${name}" -- cat | grep abc
  }

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

  lxc stop "${name}" --force
  lxc delete "${name}"
}
