test_migration() {
  # setup a second LXD
  local LXD2_DIR LXD2_ADDR lxd_backend
  # shellcheck disable=2153
  lxd_backend=$(storage_backend "$LXD_DIR")

  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  # workaround for kernel/criu
  umount /sys/kernel/debug >/dev/null 2>&1 || true

  token="$(lxc config trust add --name foo -q)"
  # shellcheck disable=2153
  lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --token "${token}"

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --token "${token}"

  migration "$LXD2_DIR"

  # This should only run on lvm and when the backend is not random. Otherwise
  # we might perform existence checks for files or dirs that won't be available
  # since the logical volume is not mounted when the container is not running.
  # shellcheck disable=2153
  if [ "${LXD_BACKEND}" = "lvm" ]; then
    # Test that non-thinpool lvm backends work fine with migration.

    local storage_pool1 storage_pool2
    # shellcheck disable=2153
    storage_pool1="lxdtest-$(basename "${LXD_DIR}")-non-thinpool-lvm-migration"
    storage_pool2="lxdtest-$(basename "${LXD2_DIR}")-non-thinpool-lvm-migration"
    lxc_remote storage create l1:"$storage_pool1" lvm lvm.use_thinpool=false size=1GiB volume.size=25MiB
    lxc_remote profile device set l1:default root pool "$storage_pool1"

    lxc_remote storage create l2:"$storage_pool2" lvm lvm.use_thinpool=false size=1GiB volume.size=25MiB
    lxc_remote profile device set l2:default root pool "$storage_pool2"

    migration "$LXD2_DIR"

    lxc_remote profile device set l1:default root pool "lxdtest-$(basename "${LXD_DIR}")"
    lxc_remote profile device set l2:default root pool "lxdtest-$(basename "${LXD2_DIR}")"

    lxc_remote storage delete l1:"$storage_pool1"
    lxc_remote storage delete l2:"$storage_pool2"
  fi

  if [ "${LXD_BACKEND}" = "zfs" ]; then
    # Test that block mode zfs backends work fine with migration.
    for fs in "ext4" "btrfs" "xfs"; do
      if ! command -v "mkfs.${fs}" >/dev/null 2>&1; then
        echo "==> SKIP: Skipping block mode test on ${fs} due to missing tools."
        continue
      fi

      local storage_pool1 storage_pool2
      # shellcheck disable=2153
      storage_pool1="lxdtest-$(basename "${LXD_DIR}")-block-mode"
      storage_pool2="lxdtest-$(basename "${LXD2_DIR}")-block-mode"
      lxc_remote storage create l1:"$storage_pool1" zfs size=1GiB volume.zfs.block_mode=true volume.block.filesystem="${fs}"
      lxc_remote profile device set l1:default root pool "$storage_pool1"

      lxc_remote storage create l2:"$storage_pool2" zfs size=1GiB volume.zfs.block_mode=true volume.block.filesystem="${fs}"
      lxc_remote profile device set l2:default root pool "$storage_pool2"

      migration "$LXD2_DIR"

      lxc_remote profile device set l1:default root pool "lxdtest-$(basename "${LXD_DIR}")"
      lxc_remote profile device set l2:default root pool "lxdtest-$(basename "${LXD2_DIR}")"

      lxc_remote storage delete l1:"$storage_pool1"
      lxc_remote storage delete l2:"$storage_pool2"
    done
  fi

  lxc_remote remote remove l1
  lxc_remote remote remove l2
  kill_lxd "$LXD2_DIR"
}

migration() {
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

  # Get container's pool.
  pool=$(lxc config profile device get default root pool)
  remote_pool=$(lxc_remote config profile device get l2:default root pool)

  # Test container only copies
  lxc init testimage cccp

  lxc storage volume set "${pool}" container/cccp user.foo=snap0
  echo "before" | lxc file push - cccp/blah
  lxc snapshot cccp
  lxc storage volume set "${pool}" container/cccp user.foo=snap1
  lxc snapshot cccp
  echo "after" | lxc file push - cccp/blah
  lxc storage volume set "${pool}" container/cccp user.foo=postsnap1

  # Check storage volume creation times are set.
  lxc query /1.0/storage-pools/"${pool}"/volumes/container/cccp | jq .created_at | grep -Fv '0001-01-01T00:00:00Z'
  lxc query /1.0/storage-pools/"${pool}"/volumes/container/cccp/snapshots/snap0 | jq .created_at | grep -Fv '0001-01-01T00:00:00Z'

  # Local container only copy.
  lxc copy cccp udssr --instance-only
  [ "$(lxc info udssr | grep -c snap)" -eq 0 ]
  [ "$(lxc file pull udssr/blah -)" = "after" ]
  lxc delete udssr

  # Local container with snapshots copy.
  lxc copy cccp udssr
  [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
  [ "$(lxc file pull udssr/blah -)" = "after" ]
  lxc storage volume show "${pool}" container/udssr
  [ "$(lxc storage volume get "${pool}" container/udssr user.foo)" = "postsnap1" ]
  [ "$(lxc storage volume get "${pool}" container/udssr/snap0 user.foo)" = "snap0" ]
  [ "$(lxc storage volume get "${pool}" container/udssr/snap1 user.foo)" = "snap1" ]
  lxc delete udssr

  # Remote container only copy.
  lxc_remote copy l1:cccp l2:udssr --instance-only
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  [ "$(lxc_remote file pull l2:udssr/blah -)" = "after" ]
  lxc_remote delete l2:udssr

  # Remote container with snapshots copy.
  lxc_remote copy l1:cccp l2:udssr
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  [ "$(lxc_remote file pull l2:udssr/blah -)" = "after" ]
  lxc_remote storage volume show l2:"${remote_pool}" container/udssr
  [ "$(lxc_remote storage volume get l2:"${remote_pool}" container/udssr user.foo)" = "postsnap1" ]
  [ "$(lxc_remote storage volume get l2:"${remote_pool}" container/udssr/snap0 user.foo)" = "snap0" ]
  [ "$(lxc_remote storage volume get l2:"${remote_pool}" container/udssr/snap1 user.foo)" = "snap1" ]
  lxc_remote delete l2:udssr

  # Remote container only move.
  lxc_remote move l1:cccp l2:udssr --instance-only --mode=relay
  ! lxc_remote info l1:cccp || false
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  lxc_remote delete l2:udssr

  lxc_remote init testimage l1:cccp
  lxc_remote snapshot l1:cccp
  lxc_remote snapshot l1:cccp

  # Remote container with snapshots move.
  lxc_remote move l1:cccp l2:udssr --mode=push
  ! lxc_remote info l1:cccp || false
  [ "$(lxc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  lxc_remote delete l2:udssr

  # Test container only copies
  lxc init testimage cccp
  lxc snapshot cccp
  lxc snapshot cccp

  # Local container with snapshots move.
  lxc move cccp udssr --mode=pull
  ! lxc info cccp || false
  [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
  lxc delete udssr

  if [ "$lxd_backend" = "zfs" ]; then
    # Test container only copies when zfs.clone_copy is set to false.
    lxc storage set "lxdtest-$(basename "${LXD_DIR}")" zfs.clone_copy false
    lxc init testimage cccp
    lxc snapshot cccp
    lxc snapshot cccp

    # Test container only copies when zfs.clone_copy is set to false.
    lxc copy cccp udssr --instance-only
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
  ! lxc file pull l1:c1 -- /root/testfile1 || false
  ! lxc file pull l2:c2 -- /root/testfile1 || false

  #lxc_remote start l1:c1 l2:c2

  # Containers may not be running when refreshing
  ! lxc_remote copy l1:c1 l2:c2 --refresh || false

  # Create test file in c1
  echo test | lxc_remote file push - l1:c1/root/testfile1

  lxc_remote stop -f l1:c1 l2:c2

  # Refresh the container and validate the contents
  lxc_remote copy l1:c1 l2:c2 --refresh
  lxc_remote start l2:c2
  lxc_remote file pull l2:c2/root/testfile1 .
  [ "$(cat testfile1)" = "test" ]
  rm testfile1
  lxc_remote stop -f l2:c2

  # Change the files modification time by adding one nanosecond.
  # Perform the change on the test runner since the busybox instances `touch` doesn't support setting nanoseconds.
  lxc_remote start l1:c1
  c1_pid="$(lxc_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  mtime_old="$(stat -c %y "/proc/${c1_pid}/root/root/testfile1")"
  mtime_old_ns="$(date -d "$mtime_old" +%N | sed 's/^0*//')"

  # Ensure the final nanoseconds are padded with zeros to create a valid format.
  mtime_new_ns="$(printf "%09d\n" "$((mtime_old_ns+1))")"
  mtime_new="$(date -d "$mtime_old" "+%Y-%m-%d %H:%M:%S.${mtime_new_ns} %z")"
  lxc_remote stop -f l1:c1

  # Before setting the new mtime create a local copy too.
  lxc_remote copy l1:c1 l1:c2

  # Change the modification time.
  lxc_remote start l1:c1
  c1_pid="$(lxc_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  touch -m -d "$mtime_new" "/proc/${c1_pid}/root/root/testfile1"
  lxc_remote stop -f l1:c1

  # Starting from rsync 3.1.3 it should discover the change of +1 nanosecond.
  # Check if the file got refreshed to a different remote.
  lxc_remote copy l1:c1 l2:c2 --refresh
  lxc_remote start l1:c1 l2:c2
  c1_pid="$(lxc_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  c2_pid="$(lxc_remote query l2:/1.0/instances/c2?recursion=1 | jq -r .state.pid)"
  [ "$(stat "/proc/${c1_pid}/root/root/testfile1" -c %y)" = "$(stat "/proc/${c2_pid}/root/root/testfile1" -c %y)" ]
  lxc_remote stop -f l1:c1 l2:c2

  # Check if the file got refreshed locally.
  lxc_remote copy l1:c1 l1:c2 --refresh
  lxc_remote start l1:c1 l1:c2
  c1_pid="$(lxc_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  c2_pid="$(lxc_remote query l1:/1.0/instances/c2?recursion=1 | jq -r .state.pid)"
  [ "$(stat "/proc/${c1_pid}/root/root/testfile1" -c %y)" = "$(stat "/proc/${c2_pid}/root/root/testfile1" -c %y)" ]
  lxc_remote rm -f l1:c2
  lxc_remote stop -f l1:c1

  # This will create snapshot c1/snap0 with test device and expiry date.
  lxc_remote config device add l1:c1 testsnapdev none
  lxc_remote config set l1:c1 snapshots.expiry '1d'
  lxc_remote snapshot l1:c1
  lxc_remote config device remove l1:c1 testsnapdev
  lxc_remote config device add l1:c1 testdev none

  # Remove the testfile from c1 and refresh again
  lxc_remote file delete l1:c1/root/testfile1
  lxc_remote copy l1:c1 l2:c2 --refresh --instance-only
  lxc_remote start l2:c2
  ! lxc_remote file pull l2:c2/root/testfile1 . || false
  lxc_remote stop -f l2:c2

  # Check whether snapshot c2/snap0 has been created with its config intact.
  ! lxc_remote config show l2:c2/snap0 || false
  lxc_remote copy l1:c1 l2:c2 --refresh
  lxc_remote ls l2:
  lxc_remote config show l2:c2/snap0
  ! lxc_remote config show l2:c2/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false
  lxc_remote config device get l2:c2 testdev type | grep -q 'none'
  lxc_remote restore l2:c2 snap0
  lxc_remote config device get l2:c2 testsnapdev type | grep -q 'none'

  # This will create snapshot c2/snap1
  lxc_remote snapshot l2:c2
  lxc_remote config show l2:c2/snap1

  # This should remove c2/snap1
  lxc_remote copy l1:c1 l2:c2 --refresh
  ! lxc_remote config show l2:c2/snap1 || false

  lxc_remote rm -f l1:c1 l2:c2

  remote_pool1="lxdtest-$(basename "${LXD_DIR}")"
  remote_pool2="lxdtest-$(basename "${lxd2_dir}")"

  lxc_remote storage volume create l1:"$remote_pool1" vol1
  lxc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snap0vol1
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol1
  lxc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snap1vol1
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol1
  lxc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=postsnap1vol1

  # remote storage volume and snapshots migration in "pull" mode
  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2"
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2 user.foo)" = "postsnap1vol1" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo)" = "snap0vol1" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo)" = "snap1vol1" ]

  # check copied volume and snapshots have different UUIDs
  [ "$(lxc_remote storage volume get l1:"$remote_pool1" vol1 volatile.uuid)" != "$(lxc_remote storage volume get l2:"$remote_pool2" vol2 volatile.uuid)" ]
  [ "$(lxc_remote storage volume get l1:"$remote_pool1" vol1/snap0 volatile.uuid)" != "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap0 volatile.uuid)" ]
  [ "$(lxc_remote storage volume get l1:"$remote_pool1" vol1/snap1 volatile.uuid)" != "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap1 volatile.uuid)" ]

  lxc_remote storage volume delete l2:"$remote_pool2" vol2

  # check moving volume and snapshots.
  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l1:"$remote_pool1/vol2"
  lxc_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol3"
  ! lxc_remote storage volume show l1:"$remote_pool1" vol2 || false
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol3 user.foo)" = "postsnap1vol1" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol3/snap0 user.foo)" = "snap0vol1" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol3/snap1 user.foo)" = "snap1vol1" ]
  lxc_remote storage volume delete l2:"$remote_pool2" vol3

  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --volume-only
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2 user.foo)" = "postsnap1vol1" ]
  ! lxc_remote storage volume show l2:"$remote_pool2" vol2/snap0 || false
  ! lxc_remote storage volume show l2:"$remote_pool2" vol2/snap1 || false
  lxc_remote storage volume delete l2:"$remote_pool2" vol2

  # remote storage volume and snapshots migration refresh in "pull" mode
  lxc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snapremovevol1
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol1 snapremove
  lxc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=postsnap1vol1
  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --refresh
  lxc_remote storage volume delete l1:"$remote_pool1" vol1

  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2 user.foo)" = "postsnap1vol1" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo)" = "snap0vol1" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo)" = "snap1vol1" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snapremove user.foo)" = "snapremovevol1" ]

  # check remote storage volume refresh from a different volume
  lxc_remote storage volume create l1:"$remote_pool1" vol3
  lxc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap0vol3
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol3
  lxc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap1vol3
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol3
  lxc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap2vol3
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol3
  lxc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=postsnap1vol3

  # check snapshot volumes and snapshots are refreshed
  lxc_remote storage volume copy l1:"$remote_pool1/vol3" l2:"$remote_pool2/vol2" --refresh
  lxc_remote storage volume ls l2:"$remote_pool2"
  lxc_remote storage volume delete l1:"$remote_pool1" vol3
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2 user.foo)" = "postsnap1vol1" ] # FIXME Should be postsnap1vol3
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo)" = "snap0vol3" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo)" = "snap1vol3" ]
  [ "$(lxc_remote storage volume get l2:"$remote_pool2" vol2/snap2 user.foo)" = "snap2vol3" ]
  ! lxc_remote storage volume show l2:"$remote_pool2" vol2/snapremove || false
  lxc_remote storage volume delete l2:"$remote_pool2" vol2

  # check that a refresh doesn't change the volume's and snapshot's UUID.
  lxc_remote storage volume create l1:"$remote_pool1" vol1
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol1
  lxc_remote storage volume copy l1:"$remote_pool1"/vol1 l2:"$remote_pool2"/vol2
  old_uuid="$(lxc storage volume get l2:"$remote_pool2" vol2 volatile.uuid)"
  old_snap0_uuid="$(lxc storage volume get l2:"$remote_pool2" vol2/snap0 volatile.uuid)"
  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --refresh
  [ "$(lxc storage volume get l2:"$remote_pool2" vol2 volatile.uuid)" = "${old_uuid}" ]
  [ "$(lxc storage volume get l2:"$remote_pool2" vol2/snap0 volatile.uuid)" = "${old_snap0_uuid}" ]
  lxc_remote storage volume delete l2:"$remote_pool2" vol2
  lxc_remote storage volume delete l1:"$remote_pool1" vol1

  # remote storage volume migration in "push" mode
  lxc_remote storage volume create l1:"$remote_pool1" vol1
  lxc_remote storage volume create l1:"$remote_pool1" vol2
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol2

  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=push
  lxc_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=push
  ! lxc_remote storage volume list l1:"$remote_pool1/vol1" || false
  lxc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol4" --volume-only --mode=push
  lxc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol5" --mode=push
  lxc_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol6" --mode=push

  lxc_remote storage volume delete l2:"$remote_pool2" vol2
  lxc_remote storage volume delete l2:"$remote_pool2" vol3
  lxc_remote storage volume delete l2:"$remote_pool2" vol4
  lxc_remote storage volume delete l2:"$remote_pool2" vol5
  lxc_remote storage volume delete l2:"$remote_pool2" vol6

  # remote storage volume migration in "relay" mode
  lxc_remote storage volume create l1:"$remote_pool1" vol1
  lxc_remote storage volume create l1:"$remote_pool1" vol2
  lxc_remote storage volume snapshot l1:"$remote_pool1" vol2

  lxc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=relay
  lxc_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=relay
  ! lxc_remote storage volume list l1:"$remote_pool1/vol1" || false
  lxc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol4" --volume-only --mode=relay
  lxc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol5" --mode=relay
  lxc_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol6" --mode=relay

  lxc_remote storage volume delete l2:"$remote_pool2" vol2
  lxc_remote storage volume delete l2:"$remote_pool2" vol3
  lxc_remote storage volume delete l2:"$remote_pool2" vol4
  lxc_remote storage volume delete l2:"$remote_pool2" vol5
  lxc_remote storage volume delete l2:"$remote_pool2" vol6

  # Test migration when rsync compression is disabled
  lxc_remote storage set l1:"$remote_pool1" rsync.compression false
  lxc_remote storage volume create l1:"$remote_pool1" foo
  lxc_remote storage volume copy l1:"$remote_pool1"/foo l2:"$remote_pool2"/bar
  lxc_remote storage volume delete l1:"$remote_pool1" foo
  lxc_remote storage volume delete l2:"$remote_pool2" bar
  lxc_remote storage unset l1:"$remote_pool1" rsync.compression

  # Test some migration between projects
  lxc_remote project create l1:proj -c features.images=false -c features.profiles=false
  lxc_remote project switch l1:proj

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
  lxc_remote delete l2:c1 -f

  lxc_remote project switch l1:default
  lxc_remote project delete l1:proj

  # Check snapshot creation dates after migration.
  lxc_remote init testimage l1:c1
  lxc_remote snapshot l1:c1
  ! lxc_remote storage volume show "l1:${remote_pool1}" container/c1 | grep -q '^created_at: 0001-01-01T00:00:00Z' || false
  ! lxc_remote storage volume show "l1:${remote_pool1}" container/c1/snap0 | grep -q '^created_at: 0001-01-01T00:00:00Z' || false
  lxc_remote copy l1:c1 l2:c1
  ! lxc_remote storage volume show "l2:${remote_pool2}" container/c1 | grep -q '^created_at: 0001-01-01T00:00:00Z' || false
  [ "$(lxc_remote storage volume show "l1:${remote_pool1}" container/c1/snap0 | awk /created_at:/)" = "$(lxc_remote storage volume show "l2:${remote_pool2}" container/c1/snap0 | awk /created_at:/)" ]
  lxc_remote delete l1:c1 -f
  lxc_remote delete l2:c1 -f

  # Check migration with invalid snapshot config (disks attached with missing source pool and source path).
  lxc_remote init testimage l1:c1
  lxc_remote storage create l1:dir dir
  lxc_remote storage volume create l1:dir vol1
  lxc_remote storage volume attach l1:dir vol1 c1 /mnt
  mkdir "$LXD_DIR/testvol2"
  lxc_remote config device add l1:c1 vol2 disk source="$LXD_DIR/testvol2" path=/vol2
  lxc_remote snapshot l1:c1 # Take snapshot with disk devices still attached.
  lxc_remote config device remove c1 vol1
  lxc_remote config device remove c1 vol2
  rmdir "$LXD_DIR/testvol2"
  lxc_remote copy l1:c1 l2:
  lxc_remote info l2:c1 | grep snap0
  lxc_remote delete l1:c1 -f
  lxc_remote delete l2:c1 -f
  lxc_remote storage volume delete l1:dir vol1
  lxc_remote storage delete l1:dir

  # Test optimized refresh
  lxc_remote init testimage l1:c1
  echo test | lxc_remote file push - l1:c1/tmp/foo
  lxc_remote copy l1:c1 l2:c1
  lxc_remote file pull l2:c1/tmp/foo .
  lxc_remote snapshot l1:c1
  echo test | lxc_remote file push - l1:c1/tmp/bar
  lxc_remote copy l1:c1 l2:c1 --refresh
  lxc_remote start l2:c1
  lxc_remote file pull l2:c1/tmp/foo .
  lxc_remote file pull l2:c1/tmp/bar .
  lxc_remote stop l2:c1 -f

  lxc_remote restore l2:c1 snap0
  lxc_remote start l2:c1
  lxc_remote file pull l2:c1/tmp/foo .
  ! lxc_remote file pull l2:c1/tmp/bar . ||  false
  lxc_remote stop l2:c1 -f

  rm foo bar

  lxc_remote rm l1:c1
  lxc_remote rm l2:c1

  lxc_remote init testimage l1:c1
  # This creates snap0
  lxc_remote snapshot l1:c1
  # This creates snap1
  lxc_remote snapshot l1:c1
  lxc_remote copy l1:c1 l2:c1
  # This creates snap2
  lxc_remote snapshot l1:c1

  # Delete first snapshot from target
  lxc_remote rm l2:c1/snap0

  # Refresh
  lxc_remote copy l1:c1 l2:c1 --refresh

  lxc_remote rm -f l1:c1
  lxc_remote rm -f l2:c1

  # In this scenario the source LXD server used to crash due to a missing slice check.
  # Let's test this to make sure it doesn't happen again.
  lxc_remote init testimage l1:c1
  lxc_remote copy l1:c1 l2:c1
  lxc_remote snapshot l1:c1
  lxc_remote snapshot l1:c1

  lxc_remote copy l1:c1 l2:c1 --refresh
  lxc_remote copy l1:c1 l2:c1 --refresh

  lxc_remote rm -f l1:c1
  lxc_remote rm -f l2:c1

  # On btrfs, this used to cause a failure because btrfs couldn't find the parent subvolume.
  lxc_remote init testimage l1:c1
  lxc_remote copy l1:c1 l2:c1
  lxc_remote snapshot l1:c1
  lxc_remote copy l1:c1 l2:c1 --refresh
  lxc_remote snapshot l1:c1
  lxc_remote copy l1:c1 l2:c1 --refresh

  lxc_remote rm -f l1:c1
  lxc_remote rm -f l2:c1

  # On zfs, this used to crash due to a websocket read issue.
  lxc launch testimage c1
  lxc snapshot c1
  lxc copy c1 l2:c1 --stateless
  lxc copy c1 l2:c1 --stateless --refresh

  lxc_remote rm -f l1:c1
  lxc_remote rm -f l2:c1

  # migrate ISO custom volumes
  truncate -s 8MiB foo.iso
  lxc storage volume import l1:"${pool}" ./foo.iso iso1
  lxc storage volume copy l1:"${pool}"/iso1 l2:"${remote_pool}"/iso1

  lxc storage volume show l2:"${remote_pool}" iso1 | grep -q 'content_type: iso'
  lxc storage volume move l1:"${pool}"/iso1 l2:"${remote_pool}"/iso2
  lxc storage volume show l2:"${remote_pool}" iso2 | grep -q 'content_type: iso'
  ! lxc storage volume show l1:"${pool}" iso1 || false

  lxc storage volume delete l2:"${remote_pool}" iso1
  lxc storage volume delete l2:"${remote_pool}" iso2
  rm -f foo.iso

  if ! command -v criu >/dev/null 2>&1; then
    echo "==> SKIP: live migration with CRIU (missing binary)"
    return
  fi

  echo "==> CRIU: starting testing live-migration"
  lxc_remote launch testimage l1:migratee -c raw.lxc=lxc.console.path=none

  # Wait for the container to be done booting
  sleep 1

  # Test stateful stop
  lxc_remote stop --stateful l1:migratee
  lxc_remote start l1:migratee

  # Test stateful snapshots
  # There is apparently a bug in CRIU that prevents checkpointing an instance that has been started from a
  # checkpoint. So stop instance first before taking stateful snapshot.
  lxc_remote stop -f l1:migratee
  lxc_remote start l1:migratee
  lxc_remote snapshot --stateful l1:migratee
  lxc_remote restore l1:migratee snap0

  # Test live migration of container
  # There is apparently a bug in CRIU that prevents checkpointing an instance that has been started from a
  # checkpoint. So stop instance first before taking stateful snapshot.
  lxc_remote stop -f l1:migratee
  lxc_remote start l1:migratee
  lxc_remote move l1:migratee l2:migratee

  # Test copy of stateful snapshot
  lxc_remote copy l2:migratee/snap0 l1:migratee
  ! lxc_remote copy l2:migratee/snap0 l1:migratee-new-name || false

  # Test stateless copies
  lxc_remote copy --stateless l2:migratee/snap0 l1:migratee-new-name

  # Cleanup
  lxc_remote delete --force l1:migratee
  lxc_remote delete --force l2:migratee
  lxc_remote delete --force l1:migratee-new-name
}
