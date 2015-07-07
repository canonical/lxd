test_migration() {
  (echo y;  sleep 3;  echo foo) | lxc remote add l1 127.0.0.1:18443 $debug
  (echo y;  sleep 3;  echo foo) | lxc remote add l2 127.0.0.1:18444 $debug

  lxc init testimage nonlive
  lxc move l1:nonlive l2:
  [ -d "$LXD2_DIR/lxc/nonlive/rootfs" ]
  [ ! -d "$LXD_DIR/lxc/nonlive" ]

  lxc copy l2:nonlive l1:nonlive2
  [ -d "$LXD_DIR/lxc/nonlive2" ]
  [ -d "$LXD2_DIR/lxc/nonlive/rootfs" ]

  lxc copy l2:nonlive l2:nonlive2

  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  lxc start l1:nonlive2
  lxc list l1: | grep RUNNING | grep nonlive2
  lxc stop l1:nonlive2 --force

  lxc start l2:nonlive
  lxc list l2: | grep RUNNING | grep nonlive
  lxc stop l2:nonlive --force

  if ! type criu >/dev/null 2>&1; then
      echo "==> SKIP: live migration with CRIU (missing binary)"
      return
  fi

  lxc launch testimage migratee

  lxc move l1:migratee l2:migratee
  lxc stop l2:migratee --force
}
