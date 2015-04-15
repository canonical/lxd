test_concurrent() {
  spawn_container() {
    name=concurrent-${1}

    lxc launch testimage ${name}
    lxc list ${name} | grep RUNNING
    echo abc | lxc exec ${name} -- cat | grep abc
    lxc stop ${name} --force
    lxc delete ${name}
  }

  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  PIDS=""

  for id in $(seq 50); do
     spawn_container $id &
     PIDS="$PIDS $!"
  done

  for pid in $PIDS; do
      wait $pid || true
  done

  ! lxc list | grep -q concurrent
}
