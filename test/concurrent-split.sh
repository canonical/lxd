test_concurrent_split() {
  if ! lxc image alias list | grep -q ^testimage$; then
      if [ -e "$LXD_TEST_IMAGE" ]; then
          lxc image import $LXD_TEST_IMAGE --alias testimage
      else
          ../scripts/lxd-images import busybox --alias testimage
      fi
  fi

  spawn_container() {
    set -e

    name=concurrent-${1}

    lxc launch testimage ${name}
    lxc list ${name} | grep RUNNING
    echo abc | lxc exec ${name} -- cat | grep abc
  }

  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  PIDS=""

  for id in $(seq 50); do
     spawn_container $id 2>&1 | tee $LXD_DIR/lxc-${id}.out &
     PIDS="$PIDS $!"
  done

  for pid in $PIDS; do
      wait $pid
  done

  read -p "all containers started;  i am $$; hit any key to continue" x
  for id in $(seq 50); do
    name=concurrent-${id}
    lxc stop ${name} --force
    lxc delete ${name}
  done

  ! lxc list | grep -q concurrent
}
