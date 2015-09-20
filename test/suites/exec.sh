test_concurrent_exec() {
  if [ -n "${TRAVIS_PULL_REQUEST:-}" ]; then
    return
  fi

  ensure_import_testimage

  name=x1
  lxc launch testimage x1
  lxc list ${name} | grep RUNNING

  exec_container() {
    echo abc$1 | lxc exec ${name} -- cat | grep abc
  }

  PIDS=""
  for i in `seq 1 50`; do
    exec_container $i 2>&1 > $LXD_DIR/exec-$i.out &
    PIDS="$PIDS $!"
  done

  for pid in $PIDS; do
    wait $pid
  done

  lxc stop ${name} --force
  lxc delete ${name}
}
