test_migration() {
  export LXD2_DIR=$(mktemp -d -p $(pwd))
  chmod 777 "${LXD2_DIR}"
  spawn_lxd 127.0.0.1:8444 "${LXD2_DIR}"
  lxd2_pid=$!

  (echo y; sleep 3; echo foo) | lxc remote add lxd2 127.0.0.1:8444

  lxc image list
  lxc init testimage nonlive
  lxc move local:nonlive lxd2:

  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  lxc start lxd2:nonlive
  lxc list lxd2: | grep RUNNING | grep nonlive
  lxc stop lxd2:nonlive --force
  [ -d "$LXD2_DIR/lxc/nonlive/rootfs" ]

  if [ -z "$(which criu)" ]; then
      echo "==> Skipping live migration tests; no criu binary found"
      return
  fi

  lxc launch testimage migratee

  lxc move migratee lxd2:migratee
  lxc stop lxd2:migratee
}
