test_concurrent_exec() {
  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  if ! lxc image alias list | grep -q "^| testimage\s*|.*$"; then
      if [ -e "$LXD_TEST_IMAGE" ]; then
          lxc image import $LXD_TEST_IMAGE --alias testimage
      else
          ../scripts/lxd-images import busybox --alias testimage
      fi
  fi

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
