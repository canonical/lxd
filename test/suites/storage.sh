test_storage() {
  ensure_import_testimage

  # shellcheck disable=2039
  local LXD_STORAGE_DIR lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false

  # edit storage and pool description
  # shellcheck disable=2039
  local storage_pool storage_volume
  storage_pool="lxdtest-$(basename "${LXD_DIR}")-pool"
  storage_volume="${storage_pool}-vol"
  lxc storage create "$storage_pool" "$lxd_backend"
  lxc storage show "$storage_pool" | sed 's/^description:.*/description: foo/' | lxc storage edit "$storage_pool"
  lxc storage show "$storage_pool" | grep -q 'description: foo'

  lxc storage volume create "$storage_pool" "$storage_volume"
  # Test resizing/applying quota to a storage volume's of type container fails.
  ! lxc storage volume set "$storage_pool" "$storage_volume" size 200MB

  # Test setting description on a storage volume
  lxc storage volume show "$storage_pool" "$storage_volume" | sed 's/^description:.*/description: bar/' | lxc storage volume edit "$storage_pool" "$storage_volume"
  lxc storage volume show "$storage_pool" "$storage_volume" | grep -q 'description: bar'

  # Validate get/set
  lxc storage set "$storage_pool" user.abc def
  [ "$(lxc storage get "$storage_pool" user.abc)" = "def" ]

  lxc storage volume set "$storage_pool" "$storage_volume" user.abc def
  [ "$(lxc storage volume get "$storage_pool" "$storage_volume" user.abc)" = "def" ]

  lxc storage volume delete "$storage_pool" "$storage_volume"
  lxc storage delete "$storage_pool"

  # Test btrfs resize
  if [ "$lxd_backend" = "lvm" ] || [ "$lxd_backend" = "ceph" ]; then
      # shellcheck disable=2039
      local btrfs_storage_pool btrfs_storage_volume
      btrfs_storage_pool="lxdtest-$(basename "${LXD_DIR}")-pool-btrfs"
      btrfs_storage_volume="${storage_pool}-vol"
      lxc storage create "$btrfs_storage_pool" "$lxd_backend" volume.block.filesystem=btrfs volume.size=200MB
      lxc storage volume create "$btrfs_storage_pool" "$btrfs_storage_volume"
      lxc storage volume show "$btrfs_storage_pool" "$btrfs_storage_volume"
      lxc storage volume set "$btrfs_storage_pool" "$btrfs_storage_volume" size 256MiB
      lxc storage volume delete "$btrfs_storage_pool" "$btrfs_storage_volume"

      # Test generation of unique UUID.
      lxc init testimage uuid1 -s "lxdtest-$(basename "${LXD_DIR}")-pool-btrfs"
      POOL="lxdtest-$(basename "${LXD_DIR}")-pool-btrfs"
      lxc copy uuid1 uuid2
      lxc start uuid1
      lxc start uuid2
      lxc stop --force uuid1
      lxc stop --force uuid2
      if [ "$lxd_backend" = "lvm" ]; then
        [ "$(blkid -s UUID -o value -p /dev/"${POOL}"/containers_uuid1)" != "$(blkid -s UUID -o value -p /dev/"${POOL}"/containers_uuid2)" ]
      elif [ "$lxd_backend" = "ceph" ]; then
        [ "$(blkid -s UUID -o value -p /dev/rbd/"${POOL}"/container_uuid1)" != "$(blkid -s UUID -o value -p /dev/rbd/"${POOL}"/container_uuid2)" ]
      fi
      lxc delete --force uuid1
      lxc delete --force uuid2
      lxc image delete testimage

      lxc storage delete "$btrfs_storage_pool"
  fi
  ensure_import_testimage

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # shellcheck disable=SC1009
    if [ "$lxd_backend" = "zfs" ]; then
    # Create loop file zfs pool.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" zfs

      # Check that we can't create a loop file in a non-LXD owned location.
      INVALID_LOOP_FILE="$(mktemp -p "${LXD_DIR}" XXXXXXXXX)-invalid-loop-file"
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" zfs source="${INVALID_LOOP_FILE}"

      # Let LXD use an already existing dataset.
      zfs create -p -o mountpoint=none "lxdtest-$(basename "${LXD_DIR}")-pool1/existing-dataset-as-pool"
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool7" zfs source="lxdtest-$(basename "${LXD_DIR}")-pool1/existing-dataset-as-pool"

      # Let LXD use an already existing storage pool.
      configure_loop_device loop_file_4 loop_device_4
      # shellcheck disable=SC2154
      zpool create -f -m none -O compression=on "lxdtest-$(basename "${LXD_DIR}")-pool9-existing-pool" "${loop_device_4}"
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool9" zfs source="lxdtest-$(basename "${LXD_DIR}")-pool9-existing-pool"

      # Let LXD create a new dataset and use as pool.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool8" zfs source="lxdtest-$(basename "${LXD_DIR}")-pool1/non-existing-dataset-as-pool"

      # Create device backed zfs pool
      configure_loop_device loop_file_1 loop_device_1
      # shellcheck disable=SC2154
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" zfs source="${loop_device_1}"

      # Test that no invalid zfs storage pool configuration keys can be set.
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-zfs-pool-config" zfs lvm.thinpool_name=bla
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-zfs-pool-config" zfs lvm.use_thinpool=false
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-zfs-pool-config" zfs lvm.vg_name=bla
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-zfs-pool-config" zfs volume.block.filesystem=ext4
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-zfs-pool-config" zfs volume.block.mount_options=discard
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-zfs-pool-config" zfs volume.size=2GB

      # Test that all valid zfs storage pool configuration keys can be set.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config" zfs volume.zfs.remove_snapshots=true
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config"

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config" zfs volume.zfs.use_refquota=true
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config"

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config" zfs zfs.clone_copy=true
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config"

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config" zfs zfs.pool_name="lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config"
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config"

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config" zfs rsync.bwlimit=1024
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-zfs-pool-config"
    fi

    if [ "$lxd_backend" = "btrfs" ]; then
      # Create loop file btrfs pool.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool3" btrfs

      # Create device backed btrfs pool.
      configure_loop_device loop_file_2 loop_device_2
      # shellcheck disable=SC2154
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool4" btrfs source="${loop_device_2}"

      # Check that we cannot create storage pools inside of ${LXD_DIR} other than ${LXD_DIR}/storage-pools/{pool_name}.
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool5_under_lxd_dir" btrfs source="${LXD_DIR}"

      # Test that no invalid btrfs storage pool configuration keys can be set.
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs lvm.thinpool_name=bla
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs lvm.use_thinpool=false
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs lvm.vg_name=bla
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs volume.block.filesystem=ext4
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs volume.block.mount_options=discard
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs volume.size=2GB
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs volume.zfs.remove_snapshots=true
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs volume.zfs.use_refquota=true
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs zfs.clone_copy=true
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-btrfs-pool-config" btrfs zfs.pool_name=bla

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-btrfs-pool-config" btrfs rsync.bwlimit=1024
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-btrfs-pool-config"

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-btrfs-pool-config" btrfs btrfs.mount_options="rw,strictatime,nospace_cache,user_subvol_rm_allowed"
      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-valid-btrfs-pool-config" btrfs.mount_options "rw,relatime,space_cache,user_subvol_rm_allowed"
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-btrfs-pool-config"
    fi

    # Create dir pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool5" dir

    # Check that we cannot create storage pools inside of ${LXD_DIR} other than ${LXD_DIR}/storage-pools/{pool_name}.
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool5_under_lxd_dir" dir source="${LXD_DIR}"

    # Check that we can create storage pools inside of ${LXD_DIR}/storage-pools/{pool_name}.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool5_under_lxd_dir" dir source="${LXD_DIR}/storage-pools/lxdtest-$(basename "${LXD_DIR}")-pool5_under_lxd_dir"

    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool5_under_lxd_dir"

    # Test that no invalid dir storage pool configuration keys can be set.
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir lvm.thinpool_name=bla
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir lvm.use_thinpool=false
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir lvm.vg_name=bla
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir size=10GB
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir volume.block.filesystem=ext4
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir volume.block.mount_options=discard
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir volume.size=2GB
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir volume.zfs.remove_snapshots=true
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir volume.zfs.use_refquota=true
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir zfs.clone_copy=true
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-dir-pool-config" dir zfs.pool_name=bla

    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-dir-pool-config" dir rsync.bwlimit=1024
    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-dir-pool-config"

    if [ "$lxd_backend" = "lvm" ]; then
      # Create lvm pool.
      configure_loop_device loop_file_3 loop_device_3
      # shellcheck disable=SC2154
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm source="${loop_device_3}" volume.size=25MB

      configure_loop_device loop_file_5 loop_device_5
      # shellcheck disable=SC2154
      # Should fail if vg does not exist, since we have no way of knowing where
      # to create the vg without a block device path set.
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool10" lvm source=dummy_vg_1 volume.size=25MB
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_5}" "${loop_device_5}"

      configure_loop_device loop_file_6 loop_device_6
      # shellcheck disable=SC2154
      pvcreate "${loop_device_6}"
      vgcreate "lxdtest-$(basename "${LXD_DIR}")-pool11-dummy_vg_2" "${loop_device_6}"
      # Reuse existing volume group "dummy_vg_2" on existing physical volume.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool11" lvm source="lxdtest-$(basename "${LXD_DIR}")-pool11-dummy_vg_2" volume.size=25MB

      configure_loop_device loop_file_7 loop_device_7
      # shellcheck disable=SC2154
      pvcreate "${loop_device_7}"
      vgcreate "lxdtest-$(basename "${LXD_DIR}")-pool12-dummy_vg_3" "${loop_device_7}"
      # Reuse existing volume group "dummy_vg_3" on existing physical volume.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool12" lvm source="lxdtest-$(basename "${LXD_DIR}")-pool12-dummy_vg_3" volume.size=25MB

      configure_loop_device loop_file_8 loop_device_8
      # shellcheck disable=SC2154
      # Create new volume group "dummy_vg_4".
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool13" lvm source="${loop_device_8}" lvm.vg_name="lxdtest-$(basename "${LXD_DIR}")-pool13-dummy_vg_4" volume.size=25MB

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool14" lvm volume.size=25MB

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" lvm lvm.use_thinpool=false volume.size=25MB

      # Test that no invalid lvm storage pool configuration keys can be set.
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-lvm-pool-config" lvm volume.zfs.remove_snapshots=true
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-lvm-pool-config" lvm volume.zfs_use_refquota=true
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-lvm-pool-config" lvm zfs.clone_copy=true
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-lvm-pool-config" lvm zfs.pool_name=bla
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-lvm-pool-config" lvm lvm.use_thinpool=false lvm.thinpool_name="lxdtest-$(basename "${LXD_DIR}")-invalid-lvm-pool-config"

      # Test that all valid lvm storage pool configuration keys can be set.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool16" lvm lvm.thinpool_name="lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config"
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool17" lvm lvm.vg_name="lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config"
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool18" lvm size=10GB
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool19" lvm volume.block.filesystem=ext4
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool20" lvm volume.block.mount_options=discard
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool21" lvm volume.size=2GB
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool22" lvm lvm.use_thinpool=true
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool23" lvm lvm.use_thinpool=true lvm.thinpool_name="lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config"
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool24" lvm rsync.bwlimit=1024
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool25" lvm volume.block.mount_options="rw,strictatime,discard"
      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool25" volume.block.mount_options "rw,lazytime"
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool26" lvm volume.block.filesystem=btrfs
    fi

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool5"

    # Import image into default storage pool.
    ensure_import_testimage

    # Muck around with some containers on various pools.
    if [ "$lxd_backend" = "zfs" ]; then
      lxc init testimage c1pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
      lxc list -c b c1pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"

      lxc init testimage c2pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
      lxc list -c b c2pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

      lxc launch testimage c3pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
      lxc list -c b c3pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"

      lxc launch testimage c4pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
      lxc list -c b c4pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1
      lxc storage volume set "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 zfs.use_refquota true
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 c1pool1 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 c1pool1 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 c1pool1
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 c1pool1

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2
      lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2-renamed
      lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2-renamed c4pool2
    fi

    if [ "$lxd_backend" = "btrfs" ]; then
      lxc init testimage c5pool3 -s "lxdtest-$(basename "${LXD_DIR}")-pool3"
      lxc list -c b c5pool3 | grep "lxdtest-$(basename "${LXD_DIR}")-pool3"
      lxc init testimage c6pool4 -s "lxdtest-$(basename "${LXD_DIR}")-pool4"
      lxc list -c b c6pool4 | grep "lxdtest-$(basename "${LXD_DIR}")-pool4"

      lxc launch testimage c7pool3 -s "lxdtest-$(basename "${LXD_DIR}")-pool3"
      lxc list -c b c7pool3 | grep "lxdtest-$(basename "${LXD_DIR}")-pool3"
      lxc launch testimage c8pool4 -s "lxdtest-$(basename "${LXD_DIR}")-pool4"
      lxc list -c b c8pool4 | grep "lxdtest-$(basename "${LXD_DIR}")-pool4"

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool3" c5pool3
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" c5pool3 c5pool3 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" c5pool3 c5pool3 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool3" c5pool3 c5pool3 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" custom/c5pool3 c5pool3 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" custom/c5pool3 c5pool3 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool3" c5pool3 c5pool3 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool4" c6pool4
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" c6pool4 c5pool3 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" c6pool4 c5pool3 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool4" c6pool4 c5pool3 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" custom/c6pool4 c5pool3 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" custom/c6pool4 c5pool3 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool4" c6pool4 c5pool3 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool3" c7pool3
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" c7pool3 c7pool3 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" c7pool3 c7pool3 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool3" c7pool3 c7pool3 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" custom/c7pool3 c7pool3 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool3" custom/c7pool3 c7pool3 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool3" c7pool3 c7pool3 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4 c8pool4 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4 c8pool4 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4 c8pool4 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" custom/c8pool4 c8pool4 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool4" custom/c8pool4 c8pool4 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4 c8pool4 testDevice
      lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4 c8pool4-renamed
      lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4-renamed c8pool4
    fi

    lxc init testimage c9pool5 -s "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc list -c b c9pool5 | grep "lxdtest-$(basename "${LXD_DIR}")-pool5"

    lxc launch testimage c11pool5 -s "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc list -c b c11pool5 | grep "lxdtest-$(basename "${LXD_DIR}")-pool5"

    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool5" c9pool5
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" c9pool5 c9pool5 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" c9pool5 c9pool5 testDevice2 /opt
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool5" c9pool5 c9pool5 testDevice
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" custom/c9pool5 c9pool5 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" custom/c9pool5 c9pool5 testDevice2 /opt
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool5" c9pool5 c9pool5 testDevice

    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5 c11pool5 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5 c11pool5 testDevice2 /opt
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5 c11pool5 testDevice
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" custom/c11pool5 c11pool5 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool5" custom/c11pool5 c11pool5 testDevice2 /opt
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5 c11pool5 testDevice
    lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5 c11pool5-renamed
    lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5-renamed c11pool5

    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool5" c12pool5
    # should create snap0
    lxc storage volume snapshot "lxdtest-$(basename "${LXD_DIR}")-pool5" c12pool5
    # should create snap1
    lxc storage volume snapshot "lxdtest-$(basename "${LXD_DIR}")-pool5" c12pool5

    if [ "$lxd_backend" = "lvm" ]; then
      lxc init testimage c10pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
      lxc list -c b c10pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"

      # Test if volume group renaming works by setting lvm.vg_name.
      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm.vg_name "lxdtest-$(basename "${LXD_DIR}")-pool6-newName"

      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm.thinpool_name "lxdtest-$(basename "${LXD_DIR}")-pool6-newThinpoolName"

      lxc launch testimage c12pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
      lxc list -c b c12pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"
      # grow lv
      lxc config device set c12pool6 root size 30MB
      lxc restart c12pool6 --force
      # shrink lv
      lxc config device set c12pool6 root size 25MB
      lxc restart c12pool6 --force

      lxc init testimage c10pool11 -s "lxdtest-$(basename "${LXD_DIR}")-pool11"
      lxc list -c b c10pool11 | grep "lxdtest-$(basename "${LXD_DIR}")-pool11"

      lxc launch testimage c12pool11 -s "lxdtest-$(basename "${LXD_DIR}")-pool11"
      lxc list -c b c12pool11 | grep "lxdtest-$(basename "${LXD_DIR}")-pool11"

      lxc init testimage c10pool12 -s "lxdtest-$(basename "${LXD_DIR}")-pool12"
      lxc list -c b c10pool12 | grep "lxdtest-$(basename "${LXD_DIR}")-pool12"

      lxc launch testimage c12pool12 -s "lxdtest-$(basename "${LXD_DIR}")-pool12"
      lxc list -c b c12pool12 | grep "lxdtest-$(basename "${LXD_DIR}")-pool12"

      lxc init testimage c10pool13 -s "lxdtest-$(basename "${LXD_DIR}")-pool13"
      lxc list -c b c10pool13 | grep "lxdtest-$(basename "${LXD_DIR}")-pool13"

      lxc launch testimage c12pool13 -s "lxdtest-$(basename "${LXD_DIR}")-pool13"
      lxc list -c b c12pool13 | grep "lxdtest-$(basename "${LXD_DIR}")-pool13"

      lxc init testimage c10pool14 -s "lxdtest-$(basename "${LXD_DIR}")-pool14"
      lxc list -c b c10pool14 | grep "lxdtest-$(basename "${LXD_DIR}")-pool14"

      lxc launch testimage c12pool14 -s "lxdtest-$(basename "${LXD_DIR}")-pool14"
      lxc list -c b c12pool14 | grep "lxdtest-$(basename "${LXD_DIR}")-pool14"

      lxc init testimage c10pool15 -s "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15"
      lxc list -c b c10pool15 | grep "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15"

      lxc launch testimage c12pool15 -s "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15"
      lxc list -c b c12pool15 | grep "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15"

      # Test that changing block filesystem works
      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool6" volume.block.filesystem xfs
      lxc init testimage c1pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool6" volume.block.filesystem btrfs
      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool6" volume.size 120MB
      lxc init testimage c2pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool6" c10pool6
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" c10pool6 c10pool6 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" c10pool6 c10pool6 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool6" c10pool6 c10pool6 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" custom/c10pool6 c10pool6 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" custom/c10pool6 c10pool6 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool6" c10pool6 c10pool6 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool6" c12pool6
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" c12pool6 c12pool6 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" c12pool6 c12pool6 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool6" c12pool6 c12pool6 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" custom/c12pool6 c12pool6 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool6" custom/c12pool6 c12pool6 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool6" c12pool6 c12pool6 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool11" c10pool11
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" c10pool11 c10pool11 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" c10pool11 c10pool11 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool11" c10pool11 c10pool11 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" custom/c10pool11 c10pool11 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" custom/c10pool11 c10pool11 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool11" c10pool11 c10pool11 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool11" c12pool11
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" c12pool11 c10pool11 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" c12pool11 c10pool11 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool11" c12pool11 c10pool11 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" custom/c12pool11 c10pool11 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool11" custom/c12pool11 c10pool11 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool11" c12pool11 c10pool11 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool12" c10pool12
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" c10pool12 c10pool12 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" c10pool12 c10pool12 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool12" c10pool12 c10pool12 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" custom/c10pool12 c10pool12 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" custom/c10pool12 c10pool12 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool12" c10pool12 c10pool12 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool12" c12pool12
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" c12pool12 c12pool12 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" c12pool12 c12pool12 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool12" c12pool12 c12pool12 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" custom/c12pool12 c12pool12 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool12" custom/c12pool12 c12pool12 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool12" c12pool12 c12pool12 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool13" c10pool13
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" c10pool13 c10pool13 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" c10pool13 c10pool13 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool13" c10pool13 c10pool13 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" custom/c10pool13 c10pool13 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" custom/c10pool13 c10pool13 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool13" c10pool13 c10pool13 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool13" c12pool13
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" c12pool13 c12pool13 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" c12pool13 c12pool13 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool13" c12pool13 c12pool13 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" custom/c12pool13 c12pool13 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool13" custom/c12pool13 c12pool13 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool13" c12pool13 c12pool13 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool14" c10pool14
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" c10pool14 c10pool14 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" c10pool14 c10pool14 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool14" c10pool14 c10pool14 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" custom/c10pool14 c10pool14 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" custom/c10pool14 c10pool14 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool14" c10pool14 c10pool14 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool14" c12pool14
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" c12pool14 c12pool14 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" c12pool14 c12pool14 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool14" c12pool14 c12pool14 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" custom/c12pool14 c12pool14 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool14" custom/c12pool14 c12pool14 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool14" c12pool14 c12pool14 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c10pool15
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" custom/c10pool15 c10pool15 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" custom/c10pool15 c10pool15 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c12pool15
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" custom/c12pool15 c12pool15 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" custom/c12pool15 c12pool15 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice
    fi

    if [ "$lxd_backend" = "zfs" ]; then
      lxc launch testimage c13pool7 -s "lxdtest-$(basename "${LXD_DIR}")-pool7"
      lxc launch testimage c14pool7 -s "lxdtest-$(basename "${LXD_DIR}")-pool7"

      lxc launch testimage c15pool8 -s "lxdtest-$(basename "${LXD_DIR}")-pool8"
      lxc launch testimage c16pool8 -s "lxdtest-$(basename "${LXD_DIR}")-pool8"

      lxc launch testimage c17pool9 -s "lxdtest-$(basename "${LXD_DIR}")-pool9"
      lxc launch testimage c18pool9 -s "lxdtest-$(basename "${LXD_DIR}")-pool9"

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool7" c13pool7
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" c13pool7 c13pool7 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" c13pool7 c13pool7 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool7" c13pool7 c13pool7 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" custom/c13pool7 c13pool7 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" custom/c13pool7 c13pool7 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool7" c13pool7 c13pool7 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool7" c14pool7
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" c14pool7 c14pool7 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" c14pool7 c14pool7 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool7" c14pool7 c14pool7 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" custom/c14pool7 c14pool7 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool7" custom/c14pool7 c14pool7 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool7" c14pool7 c14pool7 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool8" c15pool8
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" c15pool8 c15pool8 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" c15pool8 c15pool8 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool8" c15pool8 c15pool8 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" custom/c15pool8 c15pool8 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" custom/c15pool8 c15pool8 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool8" c15pool8 c15pool8 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool8" c16pool8
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" c16pool8 c16pool8 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" c16pool8 c16pool8 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool8" c16pool8 c16pool8 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" custom/c16pool8 c16pool8 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool8" custom/c16pool8 c16pool8 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool8" c16pool8 c16pool8 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool9" c17pool9
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" c17pool9 c17pool9 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" c17pool9 c17pool9 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool9" c17pool9 c17pool9 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" custom/c17pool9 c17pool9 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" custom/c17pool9 c17pool9 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool9" c17pool9 c17pool9 testDevice

      lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9 c18pool9 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9 c18pool9 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9 c18pool9 testDevice
      lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" custom/c18pool9 c18pool9 testDevice /opt
      ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool9" custom/c18pool9 c18pool9 testDevice2 /opt
      lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9 c18pool9 testDevice
      lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9 c18pool9-renamed
      lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9-renamed c18pool9
    fi

    if [ "$lxd_backend" = "zfs" ]; then
      lxc delete -f c1pool1
      lxc delete -f c3pool1

      lxc delete -f c4pool2
      lxc delete -f c2pool2

      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2
    fi

    if [ "$lxd_backend" = "btrfs" ]; then
      lxc delete -f c5pool3
      lxc delete -f c7pool3

      lxc delete -f c8pool4
      lxc delete -f c6pool4

      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool3" c5pool3
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool4" c6pool4
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool3" c7pool3
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool4" c8pool4
    fi

    lxc delete -f c9pool5
    lxc delete -f c11pool5

    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool5" c9pool5
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool5" c11pool5
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool5" c12pool5

    if [ "$lxd_backend" = "lvm" ]; then
      lxc delete -f c1pool6
      lxc delete -f c2pool6
      lxc delete -f c10pool6
      lxc delete -f c12pool6

      lxc delete -f c10pool11
      lxc delete -f c12pool11

      lxc delete -f c10pool12
      lxc delete -f c12pool12

      lxc delete -f c10pool13
      lxc delete -f c12pool13

      lxc delete -f c10pool14
      lxc delete -f c12pool14

      lxc delete -f c10pool15
      lxc delete -f c12pool15

      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool6" c10pool6
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool6"  c12pool6
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool11" c10pool11
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool11" c12pool11
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool12" c10pool12
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool12" c12pool12
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool13" c10pool13
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool13" c12pool13
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool14" c10pool14
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool14" c12pool14
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c10pool15
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" c12pool15
    fi

    if [ "$lxd_backend" = "zfs" ]; then
      lxc delete -f c13pool7
      lxc delete -f c14pool7

      lxc delete -f c15pool8
      lxc delete -f c16pool8

      lxc delete -f c17pool9
      lxc delete -f c18pool9

      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool7" c13pool7
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool7" c14pool7
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool8" c15pool8
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool8" c16pool8
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool9" c17pool9
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool9" c18pool9
    fi

    lxc image delete testimage

    if [ "$lxd_backend" = "zfs" ]; then
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool7"
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool8"
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool9"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_4}" "${loop_device_4}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool1"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool2"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    fi

    if [ "$lxd_backend" = "btrfs" ]; then
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool4"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_2}" "${loop_device_2}"
    fi

    if [ "$lxd_backend" = "lvm" ]; then
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool6"
      # shellcheck disable=SC2154
      pvremove -ff "${loop_device_3}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_3}" "${loop_device_3}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool11"
      # shellcheck disable=SC2154
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-pool11-dummy_vg_2" || true
      pvremove -ff "${loop_device_6}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_6}" "${loop_device_6}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool12"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-pool12-dummy_vg_3" || true
      pvremove -ff "${loop_device_7}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_7}" "${loop_device_7}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool13"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-pool13-dummy_vg_4" || true
      pvremove -ff "${loop_device_8}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_8}" "${loop_device_8}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool14"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-pool14" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool15" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool16"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool16" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool17"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool17" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool18"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool18" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool19"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool19" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool20"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool20" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool21"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool21" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool22"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool22" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool23"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool23" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool24"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool24" || true

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-lvm-pool-config-pool25"
      vgremove -ff "lxdtest-$(basename "${LXD_DIR}")-non-thinpool-pool25" || true
    fi
  )

  # Test applying quota
  QUOTA1="10GB"
  QUOTA2="11GB"
  if [ "$lxd_backend" = "lvm" ]; then
    QUOTA1="20MB"
    QUOTA2="21MB"
  fi

  if [ "$lxd_backend" != "dir" ] && [ "$lxd_backend" != "ceph" ]; then
    lxc launch testimage quota1
    lxc profile device set default root size "${QUOTA1}"
    lxc stop -f quota1
    lxc start quota1

    lxc launch testimage quota2
    lxc stop -f quota2
    lxc start quota2

    lxc init testimage quota3
    lxc start quota3

    lxc profile device set default root size "${QUOTA2}"

    lxc stop -f quota1
    lxc start quota1

    lxc stop -f quota2
    lxc start quota2

    lxc stop -f quota3
    lxc start quota3

    lxc profile device unset default root size
    lxc stop -f quota1
    lxc start quota1

    lxc stop -f quota2
    lxc start quota2

    lxc stop -f quota3
    lxc start quota3

    lxc delete -f quota1
    lxc delete -f quota2
    lxc delete -f quota3
  fi

  # Test removing storage pools only containing image volumes
  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  storage_pool="lxdtest-$(basename "${LXD_DIR}")-pool26"
  lxc storage create "$storage_pool" "$lxd_backend"
  lxc init -s "${storage_pool}" testimage c1
  # The storage pool will not be removed since it has c1 attached to it
  ! lxc storage delete "${storage_pool}"
  lxc delete c1
  # The storage pool will be deleted since the testimage is also attached to
  # the default pool
  lxc storage delete "${storage_pool}"
  lxc image show testimage

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
