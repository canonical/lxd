test_migration() {

  lxc init testimage nonlive
  lxc move local:nonlive lxd2:
  [ -d "$LXD2_DIR/lxc/nonlive/rootfs" ]
  [ ! -d "$LXD_DIR/lxc/nonlive" ]

  lxc copy lxd2:nonlive local:nonlive2
  [ -d "$LXD_DIR/lxc/nonlive2" ]
  [ -d "$LXD2_DIR/lxc/nonlive/rootfs" ]

  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  lxc start lxd2:nonlive
  lxc list lxd2: | grep RUNNING | grep nonlive
  lxc stop lxd2:nonlive --force

  if [ -z "$(which criu)" ]; then
      echo "==> Skipping live migration tests; no criu binary found"
      return
  fi

  lxc launch testimage migratee

  lxc move migratee lxd2:migratee
  lxc stop lxd2:migratee
}
