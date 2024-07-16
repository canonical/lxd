test_storage_driver_zfs() {
  do_storage_driver_zfs ext4
  do_storage_driver_zfs xfs
  do_storage_driver_zfs btrfs

  do_zfs_cross_pool_copy
  do_zfs_delegate
}

do_zfs_delegate() {
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" != "zfs" ]; then
    return
  fi

  if ! zfs --help | grep -q '^\s\+zone\b'; then
    echo "==> SKIP: Skipping ZFS delegation tests due as installed version doesn't support it"
    return
  fi

  # Import image into default storage pool.
  ensure_import_testimage

  # Test enabling delegation.
  storage_pool="lxdtest-$(basename "${LXD_DIR}")"

  lxc init testimage c1
  lxc storage volume set "${storage_pool}" container/c1 zfs.delegate=true
  lxc start c1

  PID=$(lxc info c1 | awk '/^PID:/ {print $2}')
  nsenter -t "${PID}" -U -- zfs list | grep -q containers/c1

  # Confirm that ZFS dataset is empty when off.
  lxc stop -f c1
  lxc storage volume unset "${storage_pool}" container/c1 zfs.delegate
  lxc start c1

  PID=$(lxc info c1 | awk '/^PID:/ {print $2}')
  ! nsenter -t "${PID}" -U -- zfs list | grep -q containers/c1

  lxc delete -f c1
}

do_zfs_cross_pool_copy() {
  local LXD_STORAGE_DIR lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" != "zfs" ]; then
    return
  fi

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
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
  lxc rm -f c1 c2 c3 c4 c5 c6
  lxc storage rm lxdtest-"$(basename "${LXD_DIR}")"-dir
  lxc storage rm lxdtest-"$(basename "${LXD_DIR}")"-zfs

  # shellcheck disable=SC2031
  kill_lxd "${LXD_STORAGE_DIR}"
}

do_storage_driver_zfs() {
  filesystem="$1"

  if ! command -v "mkfs.${filesystem}" >/dev/null 2>&1; then
    echo "==> SKIP: Skipping block mode test on ${filesystem} due to missing tools."
    return
  fi

  local LXD_STORAGE_DIR lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" != "zfs" ]; then
    return
  fi

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
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
  lxc init testimage c3 -d root,size=5GiB
  lxc delete -f c3

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

  lxc storage set lxdtest-"$(basename "${LXD_DIR}")" volume.size=5GiB
  lxc launch testimage c5
  lxc storage unset lxdtest-"$(basename "${LXD_DIR}")" volume.size

  # Clean up
  lxc rm -f c1 c3 c11 c21 c4 c5 c6 c7
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
