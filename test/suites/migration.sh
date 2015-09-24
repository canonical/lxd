test_migration() {
  ensure_import_testimage

  if ! lxc remote list | grep -q l1; then
    (echo y; sleep 3; echo foo) | lxc remote add l1 ${LXD_ADDR}
  fi
  if ! lxc remote list | grep -q l2; then
    (echo y; sleep 3; echo foo) | lxc remote add l2 ${LXD2_ADDR}
  fi

  lxc init testimage nonlive
  lxc move l1:nonlive l2:
  [ -d "${LXD2_DIR}/containers/nonlive/rootfs" ]
  [ ! -d "${LXD_DIR}/containers/nonlive" ]

  lxc copy l2:nonlive l1:nonlive2
  [ -d "${LXD_DIR}/containers/nonlive2" ]
  [ -d "${LXD2_DIR}/containers/nonlive/rootfs" ]

  lxc copy l2:nonlive l2:nonlive2
  # should have the same base image tag
  [ "`lxc config get l2:nonlive volatile.base_image`" = "`lxc config get l2:nonlive2 volatile.base_image`" ]
  # check that nonlive2 has a new addr in volatile
  [ "`lxc config get l2:nonlive volatile.eth0.hwaddr`" != "`lxc config get l2:nonlive2 volatile.eth0.hwaddr`" ]

  lxc config unset l2:nonlive volatile.base_image
  lxc copy l2:nonlive l1:nobase
  lxc delete l1:nobase

  if [ -n "${TRAVIS_PULL_REQUEST:-}" ]; then
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
