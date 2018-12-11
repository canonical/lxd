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

  # This should only run on lvm and when the backend is not random. Otherwise
  # we might perform existence checks for files or dirs that won't be available
  # since the logical volume is not mounted when the container is not running.
  # shellcheck disable=2153
  if [ "${LXD_BACKEND}" = "lvm" ]; then
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
  if [ "$lxd2_backend" != "lvm" ] && [ "$lxd2_backend" != "zfs" ] && [ "$lxd2_backend" != "ceph" ]; then
    [ -d "${lxd2_dir}/containers/nonlive/rootfs" ]
  fi
  lxc_remote stop l2:nonlive --force

  [ ! -d "${LXD_DIR}/containers/nonlive" ]
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" = "dir" ]; then
    [ -d "${lxd2_dir}/snapshots/nonlive/snap0/rootfs/bin" ]
  fi

  lxc_remote copy l2:nonlive l1:nonlive2 --mode=push
  # This line exists so that the container's storage volume is mounted when we
  # perform existence check for various files.
  lxc_remote start l2:nonlive
  [ -d "${LXD_DIR}/containers/nonlive2" ]
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ] && [ "$lxd2_backend" != "zfs" ] && [ "$lxd2_backend" != "ceph" ]; then
    [ -d "${lxd2_dir}/containers/nonlive/rootfs/bin" ]
  fi

  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ -d "${LXD_DIR}/snapshots/nonlive2/snap0/rootfs/bin" ]
  fi

  lxc_remote copy l1:nonlive2/snap0 l2:nonlive3 --mode=relay
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ] && [ "$lxd2_backend" != "zfs" ] && [ "$lxd2_backend" != "ceph" ]; then
    [ -d "${lxd2_dir}/containers/nonlive3/rootfs/bin" ]
  fi
  lxc_remote delete l2:nonlive3 --force

  lxc_remote stop l2:nonlive --force
  lxc_remote copy l2:nonlive l2:nonlive2 --mode=push
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

  lxc_remote launch testimage cccp
  lxc_remote copy l1:cccp l2:udssr --stateless
  lxc_remote delete l2:udssr --force
  lxc_remote copy l1:cccp l2:udssr --stateless --mode=push
  lxc_remote delete l2:udssr --force
  lxc_remote copy l1:cccp l2:udssr --stateless --mode=relay
  lxc_remote delete l2:udssr --force

  lxc_remote move l1:cccp l2:udssr --stateless
  lxc_remote delete l2:udssr --force
  lxc_remote launch testimage cccp
  lxc_remote move l1:cccp l2:udssr --stateless --mode=push
  lxc_remote delete l2:udssr --force
  lxc_remote launch testimage cccp
  lxc_remote move l1:cccp l2:udssr --stateless --mode=relay
  lxc_remote delete l2:udssr --force

  lxc_remote start l2:nonlive
  lxc_remote list l2: | grep RUNNING | grep nonlive
  lxc_remote delete l2:nonlive --force

  # Test container only copies
  lxc init testimage cccp
  echo "before" | lxc file push - cccp/blah
  lxc snapshot cccp
  lxc snapshot cccp
  echo "after" | lxc file push - cccp/blah

  # Local container only copy.
  lxc copy cccp udssr --container-only
  [ "$(lxc info udssr | grep -c snap)" -eq 0 ]
  [ "$(lxc file pull udssr/blah -)" = "after" ]
  lxc delete udssr

  # Local container with snapshots copy.
  lxc copy cccp udssr
  [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
  [ "$(lxc file pull udssr/blah -)" = "after" ]
  lxc delete udssr

  # Remote container only copy.
  lxc_remote copy l1:cccp l2:udssr --container-only
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  [ "$(lxc_remote file pull l2:udssr/blah -)" = "after" ]
  lxc_remote delete l2:udssr

  # Remote container with snapshots copy.
  lxc_remote copy l1:cccp l2:udssr
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  [ "$(lxc_remote file pull l2:udssr/blah -)" = "after" ]
  lxc_remote delete l2:udssr

  # Remote container only move.
  lxc_remote move l1:cccp l2:udssr --container-only --mode=relay
  ! lxc_remote info l1:cccp
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  lxc_remote delete l2:udssr

  lxc_remote init testimage l1:cccp
  lxc_remote snapshot l1:cccp
  lxc_remote snapshot l1:cccp

  # Remote container with snapshots move.
  lxc_remote move l1:cccp l2:udssr --mode=push
  ! lxc_remote info l1:cccp
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  lxc_remote delete l2:udssr

  # Test container only copies
  lxc init testimage cccp
  lxc snapshot cccp
  lxc snapshot cccp

  # Local container with snapshots move.
  lxc move cccp udssr --mode=pull
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

  lxc_remote init testimage l1:c1
  lxc_remote copy l1:c1 l2:c2
  lxc_remote copy l1:c1 l2:c2 --refresh

  lxc_remote start l1:c1 l2:c2

  # Make sure the testfile doesn't exist
  ! lxc file pull l1:c1 -- /root/testfile1
  ! lxc file pull l2:c2 -- /root/testfile1

  #lxc_remote start l1:c1 l2:c2

  # Containers may not be running when refreshing
  ! lxc_remote copy l1:c1 l2:c2 --refresh

  # Create test file in c1
  echo test | lxc_remote file push - l1:c1/root/testfile1

  lxc_remote stop -f l1:c1 l2:c2

  # Refresh the container and validate the contents
  lxc_remote copy l1:c1 l2:c2 --refresh
  lxc_remote start l2:c2
  lxc_remote file pull l2:c2/root/testfile1 .
  rm testfile1
  lxc_remote stop -f l2:c2

  # This will create snapshot c1/snap0
  lxc_remote snapshot l1:c1

  # Remove the testfile from c1 and refresh again
  lxc_remote file delete l1:c1/root/testfile1
  lxc_remote copy l1:c1 l2:c2 --refresh --container-only
  lxc_remote start l2:c2
  ! lxc_remote file pull l2:c2/root/testfile1 .
  lxc_remote stop -f l2:c2

  # Check whether snapshot c2/snap0 has been created
  ! lxc_remote config show l2:c2/snap0
  lxc_remote copy l1:c1 l2:c2 --refresh
  lxc_remote ls l2:
  lxc_remote config show l2:c2/snap0

  # This will create snapshot c2/snap1
  lxc_remote snapshot l2:c2
  lxc_remote config show l2:c2/snap1

  # This should remove c2/snap1
  lxc_remote copy l1:c1 l2:c2 --refresh
  ! lxc_remote config show l2:c2/snap1

  lxc_remote rm -f l1:c1 l2:c2

  remote_pool1="lxdtest-$(basename "${LXD_DIR}")"
  remote_pool2="lxdtest-$(basename "${lxd2_dir}")"

  lxc_remote storage volume create l1:"$remote_pool1" vol1

  # remote storage volume migration in "pull" mode
  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2"
  lxc_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3"
  ! lxc_remote storage volume list l1:"$remote_pool1/vol1"

  lxc_remote storage volume delete l2:"$remote_pool2" vol2
  lxc_remote storage volume delete l2:"$remote_pool2" vol3

  # remote storage volume migration in "push" mode
  lxc_remote storage volume create l1:"$remote_pool1" vol1

  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=push
  lxc_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=push
  ! lxc_remote storage volume list l1:"$remote_pool1/vol1"

  lxc_remote storage volume delete l2:"$remote_pool2" vol2
  lxc_remote storage volume delete l2:"$remote_pool2" vol3

  # remote storage volume migration in "relay" mode
  lxc_remote storage volume create l1:"$remote_pool1" vol1

  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=relay
  lxc_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=relay
  ! lxc_remote storage volume list l1:"$remote_pool1/vol1"

  lxc_remote storage volume delete l2:"$remote_pool2" vol2
  lxc_remote storage volume delete l2:"$remote_pool2" vol3

  # Test some migration between projects
  lxc_remote project create l1:proj -c features.images=false -c features.profiles=false
  lxc_remote project switch l1 proj

  lxc_remote init testimage l1:c1
  lxc_remote copy l1:c1 l2:
  lxc_remote start l2:c1
  lxc_remote delete l2:c1 -f

  lxc_remote snapshot l1:c1
  lxc_remote snapshot l1:c1
  lxc_remote snapshot l1:c1
  lxc_remote copy l1:c1 l2:
  lxc_remote start l2:c1
  lxc_remote stop l2:c1 -f
  lxc_remote delete l1:c1

  lxc_remote copy l2:c1 l1:
  lxc_remote start l1:c1
  lxc_remote delete l1:c1 -f

  lxc_remote delete l2:c1/snap0
  lxc_remote delete l2:c1/snap1
  lxc_remote delete l2:c1/snap2
  lxc_remote copy l2:c1 l1:
  lxc_remote start l1:c1
  lxc_remote delete l1:c1 -f
  lxc_remote delete l2:c1

  lxc_remote project switch l1 default
  lxc_remote project delete l1:proj

  if ! which criu >/dev/null 2>&1; then
    echo "==> SKIP: live migration with CRIU (missing binary)"
    return
  fi

  echo "==> CRIU: starting testing live-migration"
  lxc_remote launch testimage l1:migratee

  # Wait for the container to be done booting
  sleep 1

  # Test stateful stop
  lxc_remote stop --stateful l1:migratee
  lxc_remote start l1:migratee

  # Test stateful snapshots
  lxc_remote snapshot --stateful l1:migratee
  lxc_remote restore l1:migratee snap0

  # Test live migration of container
  lxc_remote move l1:migratee l2:migratee

  # Test copy of stateful snapshot
  lxc_remote copy l2:migratee/snap0 l1:migratee
  ! lxc_remote copy l2:migratee/snap0 l1:migratee-new-name

  # Test stateless copies
  lxc_remote copy --stateless l2:migratee/snap0 l1:migratee-new-name

  # Cleanup
  lxc_remote delete --force l1:migratee
  lxc_remote delete --force l2:migratee
  lxc_remote delete --force l1:migratee-new-name
}
