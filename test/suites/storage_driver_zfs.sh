test_storage_driver_zfs() {
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" != "zfs" ]; then
    export TEST_UNMET_REQUIREMENT="zfs specific test, not for ${lxd_backend}"
    return
  fi

  do_storage_driver_zfs ext4
  do_storage_driver_zfs xfs
  do_storage_driver_zfs btrfs

  do_zfs_cross_pool_copy
  do_zfs_delegate
  do_zfs_rebase
  do_recursive_copy_snapshot_cleanup
}

do_zfs_delegate() {
  if ! zfs --help | grep -wF "zone" >/dev/null; then
    echo "==> SKIP: Skipping ZFS delegation tests due as installed version doesn't support it"
    return
  fi

  # XXX: Ensure that `/dev/zfs` has mode 0666 so that any user on the system
  #      can interact with it. Setting those permissions is udev's job but the
  #      needed rule ships in the `zfsutils-linux` which might be installed after
  #      the kernel module is loaded and the device node created leaving it
  #      with 0600 permissions. When those permissions are not tweaked by udev,
  #      any interaction with zfs tools in the container will fail with:
  #      > Permission denied the ZFS utilities must be run as root.
  zfsPerm=$(stat -c '%a' /dev/zfs)
  if [ $((zfsPerm & 7)) -eq 0 ]; then
      chmod 0666 /dev/zfs
  fi

  # Import image into default storage pool.
  ensure_import_testimage

  # Test enabling delegation.
  storage_pool="lxdtest-$(basename "${LXD_DIR}")"

  lxc init testimage c1
  lxc storage volume set "${storage_pool}" container/c1 zfs.delegate=true
  lxc start c1

  PID="$(lxc list -f csv -c p c1)"
  nsenter -t "${PID}" -U -- zfs list | grep -wF containers/c1

  # Confirm that ZFS dataset is empty when off.
  lxc stop -f c1
  lxc storage volume unset "${storage_pool}" container/c1 zfs.delegate
  lxc start c1

  PID="$(lxc list -f csv -c p c1)"
  ! nsenter -t "${PID}" -U -- zfs list | grep -wF containers/c1 || false

  lxc delete -f c1
}

do_zfs_cross_pool_copy() {
  local LXD_STORAGE_DIR

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false

  # Import image into default storage pool.
  ensure_import_testimage

  lxc storage create lxdtest-"$(basename "${LXD_DIR}")"-dir dir

  lxc init testimage c1 -s lxdtest-"$(basename "${LXD_DIR}")"-dir
  lxc copy c1 c2 -s lxdtest-"$(basename "${LXD_DIR}")"

  # Check created zfs volume
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c2")" = "filesystem" ]

  # Turn on block mode
  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode true

  lxc copy c1 c3 -s lxdtest-"$(basename "${LXD_DIR}")"

  # Check created zfs volume
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c3")" = "volume" ]

  # Turn off block mode
  lxc storage unset lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode

  lxc storage create lxdtest-"$(basename "${LXD_DIR}")"-zfs zfs

  lxc init testimage c4 -s lxdtest-"$(basename "${LXD_DIR}")"-zfs
  lxc copy c4 c5 -s lxdtest-"$(basename "${LXD_DIR}")"

  # Check created zfs volume
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c5")" = "filesystem" ]

  # Turn on block mode
  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode true

  # Although block mode is turned on on the target storage pool, c6 will be created as a dataset.
  # That is because of optimized transfer which doesn't change the volume type.
  lxc copy c4 c6 -s lxdtest-"$(basename "${LXD_DIR}")"

  # Check created zfs volume
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c6")" = "filesystem" ]

  # Turn off block mode
  lxc storage unset lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode

  # Clean up
  lxc delete c1 c2 c3 c4 c5 c6
  lxc storage rm lxdtest-"$(basename "${LXD_DIR}")"-dir
  lxc storage rm lxdtest-"$(basename "${LXD_DIR}")"-zfs

  # shellcheck disable=SC2031
  kill_lxd "${LXD_STORAGE_DIR}"
}

do_zfs_rebase() {
  # Test ZFS rebase clone_copy mode
  local storage_pool

  storage_pool="lxdtest-$(basename "${LXD_DIR}")"

  # Ensure image is imported
  ensure_import_testimage

  # Create a source instance from the image
  lxc init testimage rebase-src
  src_ds="${storage_pool}/containers/rebase-src"
  src_origin="$(zfs get -H -o value origin "${src_ds}")"

  # Clone copy before any snapshots taken (this should create a clone with origin set to source)
  lxc storage set "${storage_pool}" zfs.clone_copy true
  lxc copy rebase-src clone-dst

  # The destination origin should be an "@copy-..." snapshot of the source.
  zfs get -H -o value origin "${storage_pool}/containers/clone-dst" | grep -F "${storage_pool}/containers/rebase-src@copy-"

  # Enable rebase mode on the pool
  lxc storage set "${storage_pool}" zfs.clone_copy rebase

  # Copy the cloned instance after enabling rebase mode
  lxc copy clone-dst rebase-dst

  # Read origin property
  dst_origin="$(zfs get -H -o value origin "${storage_pool}/containers/rebase-dst")"

  # The destination should have the same origin as the original source
  [ "${dst_origin}" = "${src_origin}" ]

  # Copy the src instance with rebase mode enabled
  lxc delete clone-dst rebase-dst
  lxc copy rebase-src rebase-dst

  # Read origin property
  dst_origin="$(zfs get -H -o value origin "${storage_pool}/containers/rebase-dst")"

  # The destination should have the same origin as the source
  [ "${dst_origin}" = "${src_origin}" ]

  # With snapshot
  lxc delete rebase-dst
  lxc snapshot rebase-src
  lxc copy rebase-src rebase-dst
  dst_origin="$(zfs get -H -o value origin "${storage_pool}/containers/rebase-dst")"

  # The destination should have the same origin as the source
  [ "${dst_origin}" = "${src_origin}" ]

  # Refresh with snapshots
  lxc snapshot rebase-src
  lxc copy rebase-src rebase-dst --refresh
  dst_origin="$(zfs get -H -o value origin "${storage_pool}/containers/rebase-dst")"

  # The destination should have the same origin as the source
  [ "${dst_origin}" = "${src_origin}" ]

  # Source snapshot copy
  lxc delete rebase-dst
  lxc copy rebase-src/snap0 rebase-dst
  dst_origin="$(zfs get -H -o value origin "${storage_pool}/containers/rebase-dst")"

  # The destination should have the same origin as the source
  [ "${dst_origin}" = "${src_origin}" ]

  # Cleanup
  lxc delete rebase-src rebase-dst

  # Unset the pool option
  lxc storage unset "${storage_pool}" zfs.clone_copy
}

do_storage_driver_zfs() {
  filesystem="$1"

  local LXD_STORAGE_DIR

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false

  # Import image into default storage pool.
  ensure_import_testimage

  fingerprint=$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')

  # Create non-block container
  lxc launch testimage c1

  # Check created container and image volumes
  zfs list lxdtest-"$(basename "${LXD_DIR}")/containers/c1"
  zfs list lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}"
  zfs list lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}@readonly"
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c1")" = "filesystem" ]
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}")" = "filesystem" ]

  # Turn on block mode
  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode true

  # Set block filesystem
  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.block.filesystem "${filesystem}"

  # Create container in block mode and check online grow.
  lxc launch testimage c2
  lxc config device override c2 root size=11GiB

  # Check created zfs volumes
  zfs list lxdtest-"$(basename "${LXD_DIR}")/containers/c2"
  zfs list lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}_${filesystem}"
  zfs list lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}_${filesystem}@readonly"
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c2")" = "volume" ]

  # Create container in block mode with smaller size override.
  lxc init testimage c3 -d root,size=1GiB
  lxc delete c3

  # Delete image volume
  lxc storage volume rm lxdtest-"$(basename "${LXD_DIR}")" image/"${fingerprint}"

  zfs list lxdtest-"$(basename "${LXD_DIR}")/deleted/images/${fingerprint}_${filesystem}"
  zfs list lxdtest-"$(basename "${LXD_DIR}")/deleted/images/${fingerprint}_${filesystem}@readonly"

  lxc storage unset lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode

  # Create non-block mode instance
  lxc launch testimage c6
  zfs list lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}"
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c6")" = "filesystem" ]

  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode true

  # Create block mode instance
  lxc launch testimage c7

  # Check created zfs volumes
  zfs list lxdtest-"$(basename "${LXD_DIR}")/containers/c7"
  zfs list lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}_${filesystem}"
  zfs list lxdtest-"$(basename "${LXD_DIR}")/images/${fingerprint}_${filesystem}@readonly"
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c7")" = "volume" ]

  lxc stop -f c1 c2

  # Try renaming instance
  lxc rename c2 c3

  # Create snapshot
  lxc snapshot c3 snap0
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c3@snapshot-snap0")" = "snapshot" ]

  # This should create c11 as a dataset, and c21 as a zvol
  lxc copy c1 c11
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c11")" = "filesystem" ]

  lxc copy c3 c21
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c21")" = "volume" ]
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c21@snapshot-snap0")" = "snapshot" ]

  # Create storage volumes
  lxc storage volume create lxdtest-"$(basename "${LXD_DIR}")" vol1
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/custom/default_vol1")" = "volume" ]

  lxc storage volume create lxdtest-"$(basename "${LXD_DIR}")" vol2 zfs.block_mode=false
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/custom/default_vol2")" = "filesystem" ]

  lxc storage volume attach lxdtest-"$(basename "${LXD_DIR}")" vol1 c1 /mnt
  lxc storage volume attach lxdtest-"$(basename "${LXD_DIR}")" vol1 c3 /mnt
  lxc storage volume attach lxdtest-"$(basename "${LXD_DIR}")" vol1 c21 /mnt

  lxc start c1
  lxc start c3
  lxc start c21

  lxc exec c3 -- touch /mnt/foo
  lxc exec c21 -- ls /mnt/foo
  lxc exec c1 -- ls /mnt/foo

  lxc storage volume detach lxdtest-"$(basename "${LXD_DIR}")" vol1 c1
  lxc storage volume detach lxdtest-"$(basename "${LXD_DIR}")" vol1 c3
  lxc storage volume detach lxdtest-"$(basename "${LXD_DIR}")" vol1 c21

  ! lxc exec c3 -- ls /mnt/foo || false
  ! lxc exec c21 -- ls /mnt/foo || false

  # Backup and import
  lxc launch testimage c4
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c4")" = "volume" ]
  lxc exec c4 -- touch /root/foo
  lxc stop -f c4
  lxc snapshot c4 snap0
  lxc export c4 "${LXD_DIR}/c4.tar.gz"
  lxc rm -f c4

  lxc import "${LXD_DIR}/c4.tar.gz" c4
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c4")" = "volume" ]
  [ "$(zfs get -H -o value type lxdtest-"$(basename "${LXD_DIR}")/containers/c4@snapshot-snap0")" = "snapshot" ]
  lxc start c4
  lxc exec c4 -- test -f /root/foo

  # Snapshot and restore
  lxc snapshot c4 snap1
  lxc exec c4 -- touch /root/bar
  lxc stop -f c4
  lxc restore c4 snap1
  lxc start c4
  lxc exec c4 -- test -f /root/foo
  ! lxc exec c4 -- test -f /root/bar || false

  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.size=1GiB
  lxc launch testimage c5
  lxc storage unset lxdtest-"$(basename "${LXD_DIR}")" volume.size

  # Test snapshot restore behavior with dependent clones
  # Enable remove_snapshots
  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.remove_snapshots true

  # Create container with multiple snapshots
  lxc launch testimage c8
  lxc snapshot c8 snap0
  lxc exec c8 -- touch /root/file1
  lxc snapshot c8 snap1
  lxc exec c8 -- touch /root/file2
  lxc snapshot c8 snap2

  # Clone from the middle snapshot (this creates a dependency)
  lxc copy c8/snap1 c9

  # Store snapshot names before restore attempt
  snap_list_before=$(lxc info c8 | awk '/^\s+snap/ {print $2}')

  # Try to restore c8 to snap0 (should fail due to dependent clone c9 on snap1)
  # This tests that snapshots are NOT removed from LXD records on failure
  ! lxc restore c8 snap0 2>&1 | grep "cannot be restored due to snapshot.*having.*dependent clone" || false

  # Verify that snapshots are still visible in LXD after failed restore
  snap_list_after=$(lxc info c8 | awk '/^\s+snap/ {print $2}')
  [ "$snap_list_before" = "$snap_list_after" ] || return 1

  # Also verify that the ZFS snapshots still exist
  for snap in snap0 snap1 snap2; do
    zfs list -H -o name "lxdtest-$(basename "${LXD_DIR}")/containers/c8@snapshot-${snap}" 2>&1 || return 1
  done

  # Delete the dependent clone to allow restoration
  lxc delete -f c9

  # Now restore should work since dependency is gone
  lxc restore c8 snap0

  # Verify c8 has no files after restore to snap0
  ! lxc exec c8 -- test -f /root/file1 || false
  ! lxc exec c8 -- test -f /root/file2 || false

  # Clean up
  lxc delete -f c8
  lxc storage unset lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.remove_snapshots

  # Clean up
  lxc delete -f c1 c3 c11 c21 c4 c5 c6 c7
  lxc storage volume rm lxdtest-"$(basename "${LXD_DIR}")" vol1
  lxc storage volume rm lxdtest-"$(basename "${LXD_DIR}")" vol2

  # Turn off block mode
  lxc storage unset lxdtest-"$(basename "${LXD_DIR}")" volume.zfs.block_mode

  # Regular (no block mode) storage pool shouldn't be allowed to set block.*.
  ! lxc storage set lxdtest-"$(basename "${LXD_DIR}")" block.filesystem=ext4 || false
  ! lxc storage set lxdtest-"$(basename "${LXD_DIR}")" block.mount_options=rw || false

  # shellcheck disable=SC2031
  kill_lxd "${LXD_STORAGE_DIR}"
}

do_recursive_copy_snapshot_cleanup() {
  echo "Test recursive copy snapshot cleanup."
  local storage_pool
  storage_pool="lxdtest-$(basename "${LXD_DIR}")"

  echo "Create the first container."
  lxc init --empty t1

  echo "Make two copies."
  lxc copy t1 t2
  lxc copy t1 t3

  echo "Verify two copy snapshots exist."
  [ "$(zfs list -t snapshot -H -o name "${storage_pool}/containers/t1" | grep -cF "@copy-")" -eq 2 ]

  echo "Delete t3, should delete one copy snapshot."
  lxc delete t3

  echo "Verify one copy snapshot remains."
  [ "$(zfs list -t snapshot -H -o name "${storage_pool}/containers/t1" | grep -cF "@copy-")" -eq 1 ]

  echo "Delete t2, should delete the remaining copy snapshot."
  lxc delete t2

  echo "Verify no snapshots remain, should output \"no datasets available\"."
  [ "$(zfs list -t snapshot "${storage_pool}/containers/t1" 2>&1)" = "no datasets available" ]

  echo "Create two new copies."
  lxc copy t1 t4
  lxc copy t1 t5

  echo "Verify two new copy snapshots exist."
  [ "$(zfs list -t snapshot -H -o name "${storage_pool}/containers/t1" | grep -cF "@copy-")" -eq 2 ]

  echo "Delete the original container t1, should move to deleted pool with snapshots."
  lxc delete t1

  echo "Verify container moved to deleted pool with both copy snapshots."
  [ "$(zfs list -rt snapshot "${storage_pool}/deleted/containers" | grep -cF "@copy-")" -eq 2 ]

  echo "Delete t5, should delete its snapshot from deleted container."
  lxc delete t5

  echo "Verify one snapshot remains in the deleted container."
  [ "$(zfs list -rt snapshot "${storage_pool}/deleted/containers" | grep -cF "@copy-")" -eq 1 ]

  echo "Delete t4, should delete remaining snapshot, leaving no snapshots."
  lxc delete t4

  echo "Verify no snapshots remain."
  [ "$(zfs list -rt snapshot "${storage_pool}/deleted/containers" 2>&1)" = "no datasets available" ]

  echo "Test chain copy snapshot cleanup."

  echo "Create base container."
  lxc init --empty base

  echo "Create chain of copies."
  lxc copy base chain1
  lxc copy chain1 chain2
  lxc copy chain2 chain3

  echo "Verify base container has one copy snapshot."
  [ "$(zfs list -t snapshot -H -o name "${storage_pool}/containers/base" | grep -cF "@copy-")" -eq 1 ]

  echo "Verify chain1 has one copy snapshot."
  [ "$(zfs list -t snapshot -H -o name "${storage_pool}/containers/chain1" | grep -cF "@copy-")" -eq 1 ]

  echo "Verify chain2 has one copy snapshot."
  [ "$(zfs list -t snapshot -H -o name "${storage_pool}/containers/chain2" | grep -cF "@copy-")" -eq 1 ]

  echo "Verify chain3 has no copy snapshots."
  [ "$(zfs list -t snapshot -H -o name "${storage_pool}/containers/chain3" | grep -cF "@copy-")" -eq 0 ]

  echo "Delete base, chain1, and chain2 containers."
  lxc delete base chain1 chain2

  echo "Verify three copy snapshots exist in deleted pool."
  [ "$(zfs list -rt snapshot "${storage_pool}/deleted/containers" | grep -cF "@copy-")" -eq 3 ]

  echo "Delete the remaining chain3 container."
  lxc delete chain3

  echo "Verify no snapshots remain in deleted pool."
  [ "$(zfs list -rt snapshot "${storage_pool}/deleted/containers" 2>&1)" = "no datasets available" ]
}
