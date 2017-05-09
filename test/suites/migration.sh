test_migration() {
  # setup a second LXD
  # shellcheck disable=2039
  local LXD2_DIR LXD2_ADDR lxd_backend
  # shellcheck disable=2153
  lxd_backend=$(storage_backend "$LXD_DIR")

  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  # workaround for kernel/criu
  umount /sys/kernel/debug >/dev/null 2>&1 || true

  if ! lxc_remote remote list | grep -q l1; then
    # shellcheck disable=2153
    lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --password foo
  fi
  if ! lxc_remote remote list | grep -q l2; then
    lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo
  fi

  migration "$LXD2_DIR"

  if [ "${lxd_backend}" = "lvm" ]; then
    # Test that non-thinpool lvm backends work fine with migration.

    # shellcheck disable=2039
    local storage_pool1 storage_pool2
    # shellcheck disable=2153
    storage_pool1="lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-migration"
    storage_pool2="lxdtest-$(basename "${LXD2_DIR}")-non-thinpool-lvm-migration"
    lxc_remote storage create l1:"$storage_pool1" lvm lvm.use_thinpool=false volume.size=25MB
    lxc_remote profile device set l1:default root pool "$storage_pool1"

    lxc_remote storage create l2:"$storage_pool2" lvm lvm.use_thinpool=false volume.size=25MB
    lxc_remote profile device set l2:default root pool "$storage_pool2"

    migration "$LXD2_DIR"

    lxc_remote profile device set l1:default root pool "lxdtest-$(basename "${LXD_DIR}")"
    lxc_remote profile device set l2:default root pool "lxdtest-$(basename "${LXD2_DIR}")"

    lxc_remote storage delete l1:"$storage_pool1"
    lxc_remote storage delete l2:"$storage_pool2"
  fi

  lxc_remote remote remove l2
  kill_lxd "$LXD2_DIR"
}

migration() {
  # shellcheck disable=2039
  local lxd2_dir lxd_backend lxd2_backend
  lxd2_dir="$1"
  lxd_backend=$(storage_backend "$LXD_DIR")
  lxd2_backend=$(storage_backend "$lxd2_dir")
  ensure_import_testimage

  lxc_remote init testimage nonlive
  # test moving snapshots
  lxc_remote config set l1:nonlive user.tester foo
  lxc_remote snapshot l1:nonlive
  lxc_remote config unset l1:nonlive user.tester
  lxc_remote move l1:nonlive l2:
  lxc_remote config show l2:nonlive/snap0 | grep user.tester | grep foo

  # This line exists so that the container's storage volume is mounted when we
  # perform existence check for various files.
  lxc_remote start l2:nonlive
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ]; then
    [ -d "${lxd2_dir}/containers/nonlive/rootfs" ]
  fi
  lxc_remote stop l2:nonlive

  [ ! -d "${LXD_DIR}/containers/nonlive" ]
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" = "dir" ]; then
    [ -d "${lxd2_dir}/snapshots/nonlive/snap0/rootfs/bin" ]
  fi

  lxc_remote copy l2:nonlive l1:nonlive2
  # This line exists so that the container's storage volume is mounted when we
  # perform existence check for various files.
  lxc_remote start l2:nonlive
  [ -d "${LXD_DIR}/containers/nonlive2" ]
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ]; then
    [ -d "${lxd2_dir}/containers/nonlive/rootfs/bin" ]
  fi

  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/nonlive2/snap0/rootfs/bin" ]
  fi

  lxc_remote copy l1:nonlive2/snap0 l2:nonlive3
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ]; then
    [ -d "${lxd2_dir}/containers/nonlive3/rootfs/bin" ]
  fi
  lxc_remote delete l2:nonlive3 --force

  lxc_remote stop l2:nonlive
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
  lxc_remote delete l1:nonlive2 l2:nonlive2 --force

  lxc_remote start l2:nonlive
  lxc_remote list l2: | grep RUNNING | grep nonlive
  lxc_remote delete l2:nonlive --force

  # Test container only copies
  lxc init testimage cccp
  lxc snapshot cccp
  lxc snapshot cccp

  # Local container only copy.
  lxc copy cccp udssr --container-only
  [ "$(lxc info udssr | grep -c snap)" -eq 0 ]
  lxc delete udssr

  # Local container with snapshots copy.
  lxc copy cccp udssr
  [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
  lxc delete udssr

  # Remote container only copy.
  lxc_remote copy l1:cccp l2:udssr --container-only
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  lxc_remote delete l2:udssr

  # Remote container with snapshots copy.
  lxc_remote copy l1:cccp l2:udssr
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  lxc_remote delete l2:udssr

  # Remote container only move.
  lxc_remote move l1:cccp l2:udssr --container-only
  ! lxc_remote info l1:cccp
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  lxc_remote delete l2:udssr

  lxc_remote init testimage l1:cccp
  lxc_remote snapshot l1:cccp
  lxc_remote snapshot l1:cccp

  # Remote container with snapshots move.
  lxc_remote move l1:cccp l2:udssr
  ! lxc_remote info l1:cccp
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  lxc_remote delete l2:udssr

  # Test container only copies
  lxc init testimage cccp
  lxc snapshot cccp
  lxc snapshot cccp

  # Local container with snapshots move.
  lxc move cccp udssr
  ! lxc info cccp
  [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
  lxc delete udssr

  if [ "$lxd_backend" = "zfs" ]; then
    # Test container only copies when zfs.clone_copy is set to false.
    lxc storage set "lxdtest-$(basename "${LXD_DIR}")" zfs.clone_copy false
    lxc init testimage cccp
    lxc snapshot cccp
    lxc snapshot cccp

    # Test container only copies when zfs.clone_copy is set to false.
    lxc copy cccp udssr --container-only
    [ "$(lxc info udssr | grep -c snap)" -eq 0 ]
    lxc delete udssr

    # Test container with snapshots copy when zfs.clone_copy is set to false.
    lxc copy cccp udssr
    [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
    lxc delete cccp
    lxc delete udssr

    lxc storage unset "lxdtest-$(basename "${LXD_DIR}")" zfs.clone_copy
  fi

  if ! which criu >/dev/null 2>&1; then
    echo "==> SKIP: live migration with CRIU (missing binary)"
    return
  fi

  lxc_remote launch testimage l1:migratee

  # let the container do some interesting things
  sleep 1s

  lxc_remote stop --stateful l1:migratee
  lxc_remote start l1:migratee
  lxc_remote delete --force l1:migratee

  lxc_remote init testimage l1:cccp
  lxc_remote snapshot l1:cccp
  lxc_remote snapshot l1:cccp

  # Remote container with snapshots live migration.
  lxc_remote start l1:cccp
  lxc_remote move l1:cccp l2:udssr
  ! lxc_remote info l1:cccp
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  lxc_remote delete l2:udssr
}
