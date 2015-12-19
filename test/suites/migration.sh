#!/bin/sh

test_migration() {
  ensure_import_testimage

  if ! lxc_remote remote list | grep -q l1; then
    lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --password foo
  fi
  if ! lxc_remote remote list | grep -q l2; then
    lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo
  fi

  lxc_remote init testimage nonlive
  # test moving snapshots
  lxc_remote snapshot l1:nonlive
  lxc_remote move l1:nonlive l2:
  # FIXME: make this backend agnostic
  if [ "${LXD_BACKEND}" != "lvm" ]; then
    [ -d "${LXD2_DIR}/containers/nonlive/rootfs" ]
  fi
  [ ! -d "${LXD_DIR}/containers/nonlive" ]
  # FIXME: make this backend agnostic
  if [ "${LXD_BACKEND}" = "dir" ]; then
    [ -d "${LXD2_DIR}/snapshots/nonlive/snap0/rootfs/bin" ]
  fi

  lxc_remote copy l2:nonlive l1:nonlive2
  [ -d "${LXD_DIR}/containers/nonlive2" ]
  # FIXME: make this backend agnostic
  if [ "${LXD_BACKEND}" != "lvm" ]; then
    [ -d "${LXD2_DIR}/containers/nonlive/rootfs/bin" ]
  fi
  # FIXME: make this backend agnostic
  if [ "${LXD_BACKEND}" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/nonlive2/snap0/rootfs/bin" ]
  fi

  lxc_remote copy l1:nonlive2/snap0 l2:nonlive3
  # FIXME: make this backend agnostic
  if [ "${LXD_BACKEND}" != "lvm" ]; then
    [ -d "${LXD2_DIR}/containers/nonlive3/rootfs/bin" ]
  fi

  lxc_remote copy l2:nonlive l2:nonlive2
  # should have the same base image tag
  [ "$(lxc_remote config get l2:nonlive volatile.base_image)" = "$(lxc_remote config get l2:nonlive2 volatile.base_image)" ]
  # check that nonlive2 has a new addr in volatile
  [ "$(lxc_remote config get l2:nonlive volatile.eth0.hwaddr)" != "$(lxc_remote config get l2:nonlive2 volatile.eth0.hwaddr)" ]

  lxc_remote config unset l2:nonlive volatile.base_image
  lxc_remote copy l2:nonlive l1:nobase
  lxc_remote delete l1:nobase

  lxc_remote start l1:nonlive2
  lxc_remote list l1: | grep RUNNING | grep nonlive2
  lxc_remote stop l1:nonlive2 --force

  lxc_remote start l2:nonlive
  lxc_remote list l2: | grep RUNNING | grep nonlive
  lxc_remote stop l2:nonlive --force

  if ! which criu >/dev/null 2>&1; then
    echo "==> SKIP: live migration with CRIU (missing binary)"
    return
  fi

  lxc_remote launch testimage migratee

  lxc_remote move l1:migratee l2:migratee
  lxc_remote stop l2:migratee --force
}
