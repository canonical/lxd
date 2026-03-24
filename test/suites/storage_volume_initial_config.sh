test_storage_volume_initial_config() {

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "${lxd_backend}" != "zfs" ] && [ "${lxd_backend}" != "lvm" ] && [ "${lxd_backend}" != "ceph" ]; then
    export TEST_UNMET_REQUIREMENT="${lxd_backend} driver does not support initial configuration for storage volumes"
    return 0
  fi

  ensure_import_testimage

  image="testimage"
  profile="profile-initial-values"
  local pool
  pool="lxdtest-$(basename "${LXD_DIR}")"

  if [ "$lxd_backend" = "zfs" ] || [ "$lxd_backend" = "lvm" ]; then
    pool="storage-initial-values"
    lxc storage create "${pool}" "${lxd_backend}" size=320MiB
  fi

  if [ "$lxd_backend" = "zfs" ]; then
    lxc storage set "${pool}" volume.zfs.block_mode=true
  fi

  lxc profile create "${profile}"
  lxc profile device add "${profile}" root disk path=/ pool="${pool}"

  lxc storage set "${pool}" volume.size=128MiB volume.block.filesystem=ext4

  # Test default configuration (without initial configuration).
  lxc init "${image}" c --profile "${profile}"
  [ "$(lxc storage volume get "${pool}" container/c block.filesystem)" = "ext4" ]
  lxc delete c

  lxc init c --empty --profile "${profile}"
  [ "$(lxc storage volume get "${pool}" container/c block.filesystem)" = "ext4" ]
  lxc delete c

  # Test profile initial configuration.
  lxc profile device set "${profile}" root initial.block.filesystem=btrfs

  lxc init "${image}" c --profile "${profile}"
  [ "$(lxc storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]
  lxc delete c

  lxc init c --empty --profile "${profile}"
  [ "$(lxc storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]
  lxc delete c

  # Test instance initial configuration.
  lxc init "${image}" c -s "${pool}" --no-profiles --device root,initial.block.filesystem=btrfs
  [ "$(lxc storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]
  lxc delete c

  lxc init c --empty -s "${pool}" --no-profiles --device root,initial.block.filesystem=btrfs
  [ "$(lxc storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]

  # Verify instance initial.* configuration modification.
  ! lxc config device set c root initial.block.mount_options=noatime || false  # NOK: Add new configuration.
  ! lxc config device set c root initial.block.filesystem=xfs || false         # NOK: Modify existing configuration.
  lxc delete c

  if [ "$lxd_backend" = "zfs" ]; then
    # Clear profile and storage options.
    lxc storage set "${pool}" volume.block.filesystem= volume.zfs.block_mode=true
    lxc profile device unset "${profile}" root initial.block.filesystem

    # > Verify zfs.block_mode without initial configuration.

    # Verify "zfs.block_mode=true" is applied from pool configuration.
    lxc init c --empty --profile "${profile}"
    [ "$(lxc storage volume get "${pool}" container/c zfs.block_mode)" = "true" ]
    lxc delete c

    # Verify "zfs.block_mode=false" is applied from pool configuration.
    lxc storage set "${pool}" volume.zfs.block_mode=false

    lxc init c --empty --profile "${profile}"
    [ "$(lxc storage volume get "${pool}" container/c zfs.block_mode)" = "false" ]
    lxc delete c


    # > Overwrite zfs.block_mode with initial configuration in profile.

    # Verify instance "initial.zfs.block_mode=true" configuration is applied.
    lxc profile device set "${profile}" root initial.zfs.block_mode=true

    lxc init c --empty --profile "${profile}"
    [ "$(lxc storage volume get "${pool}" container/c zfs.block_mode)" = "true" ]
    lxc delete c

    # Verify profile "initial.zfs.block_mode=false" configuration is applied.
    lxc storage set "${pool}" volume.zfs.block_mode=true
    lxc profile device set "${profile}" root initial.zfs.block_mode=false

    lxc init c --empty --profile "${profile}"
    [ "$(lxc storage volume get "${pool}" container/c zfs.block_mode)" = "false" ]
    lxc delete c


    # > Verify instance overwrite of initial.* configuration.

    # Verify instance "initial.zfs.block_mode=true" configuration is applied.
    lxc storage set "${pool}" volume.zfs.block_mode=false
    lxc profile device set "${profile}" root initial.zfs.block_mode=false

    lxc init c --empty --profile "${profile}" --device root,initial.zfs.block_mode=true
    [ "$(lxc storage volume get "${pool}" container/c zfs.block_mode)" = "true" ]
    lxc delete c

    # Verify instance "initial.zfs.block_mode=false" configuration is applied.
    lxc storage set "${pool}" volume.zfs.block_mode=true
    lxc profile device set "${profile}" root initial.zfs.block_mode=true

    lxc init c --empty --profile "${profile}" --device root,initial.zfs.block_mode=false
    [ "$(lxc storage volume get "${pool}" container/c zfs.block_mode)" = "false" ]
    lxc delete c


    # > Verify initial.zfs.blocksize configuration.

    # Custom blocksize.
    lxc init "${image}" c --no-profiles --storage "${pool}" --device root,initial.zfs.blocksize=64KiB
    [ "$(lxc storage volume get "${pool}" container/c zfs.blocksize)" = "64KiB" ]
    [ "$(zfs get volblocksize "${pool}/containers/c" -H -o value)" = "64K" ]
    lxc delete c

    # Custom blocksize that exceeds maximum allowed blocksize.
    lxc init "${image}" c --no-profiles --storage "${pool}" --device root,initial.zfs.blocksize=512KiB
    [ "$(lxc storage volume get "${pool}" container/c zfs.blocksize)" = "512KiB" ]
    [ "$(zfs get volblocksize "${pool}/containers/c" -H -o value)" = "128K" ]
    lxc delete c
    lxc storage unset "${pool}" volume.zfs.block_mode

    sub_test "Verify initial.zfs.promote functionality."

    [ "$(! "${_LXC}" launch "${image}" c --no-profiles --storage "${pool}" --device root,initial.zfs.promote=true 2>&1 1>/dev/null)" = 'Error: Failed creating instance from image: Cannot promote volume when creating from an image' ]
    lxc launch "${image}" c --no-profiles --storage "${pool}"
    [ "$(! "${_LXC}" config device set c root initial.zfs.promote=true 2>&1 1>/dev/null)" = 'Error: Device "root" initial configuration "initial.zfs.promote" cannot be added once the instance is created' ]

    imagesnap="${pool}/images/$(lxc config get c volatile.base_image)@readonly"
    has_origin() {
      if [ "$2" = image ]; then
        expected="^${imagesnap}\$"
      else
        expected="^${pool}/containers/$2@copy-"
      fi
      zfs get origin "${pool}/containers/$1" -H -o value | grep "$expected"
    }

    # > Check that container's origin is the base image snapshot.
    has_origin c image

    # > Create a snapshot of the container to check that after promotion is not allowed with snapshots present.
    lxc snapshot c snap0
    # > Check that a container with snapshots cannot be promoted.
    [ "$(! "${_LXC}" copy c c-save1 -d root,initial.zfs.promote=true 2>&1 1>/dev/null)" = 'Error: Create instance from copy: Cannot promote volume when source volume has snapshots or is a snapshot' ]
    lxc delete c/snap0

    # > Add some data to the container and create a ZFS promoted saved point.
    lxc exec c -- touch /root/testfile1
    lxc copy c c-save1 -d root,initial.zfs.promote=true

    # > Check that the c container has a ZFS origin of c-save1, which takes c's place.
    has_origin c c-save1
    has_origin c-save1 image

    # > Add some more data to the container and create a 2nd ZFS promoted saved point.
    lxc exec c -- touch /root/testfile2
    lxc copy c c-save2 -d root,initial.zfs.promote=true

    # > Check that the c container has a ZFS origin of c-save2, which takes c's place.
    has_origin c c-save2
    has_origin c-save2 c-save1
    has_origin c-save1 image

    # > Now create new container from the save points and check their data is present.
    lxc copy c-save1 c-restore1
    has_origin c-restore1 c-save1
    has_origin c c-save2
    has_origin c-save2 c-save1
    has_origin c-save1 image
    lxc file delete c-restore1/root/testfile1
    ! lxc file delete c-restore1/root/testfile2 || false

    lxc copy c-save2 c-restore2
    has_origin c-restore2 c-save2
    has_origin c-restore1 c-save1
    has_origin c c-save2
    has_origin c-save2 c-save1
    has_origin c-save1 image
    lxc file delete c-restore2/root/testfile1
    lxc file delete c-restore2/root/testfile2

    # > Create a clone of the c container as a backup for restore later.
    lxc stop --force c
    lxc copy c c-backup -d root,initial.zfs.promote=true

    # > Check that the c container has a ZFS origin of c-backup, which takes c's place.
    has_origin c c-backup
    has_origin c-backup c-save2
    has_origin c-restore2 c-save2
    has_origin c-restore1 c-save1
    has_origin c-save2 c-save1
    has_origin c-save1 image

    # > Now restore the container from a save point and check its data is present.
    cUUID=$(lxc config get c volatile.uuid)
    lxc copy --refresh -c "volatile.uuid=${cUUID}" c-save1 c
    [ "$(lxc config get c volatile.uuid)" = "${cUUID}" ]
    has_origin c c-save1
    has_origin c-backup c-save2
    has_origin c-restore2 c-save2
    has_origin c-restore1 c-save1
    has_origin c-save2 c-save1
    has_origin c-save1 image
    lxc start c
    lxc exec c -- test -f /root/testfile1
    lxc exec c -- test ! -f /root/testfile2

    # > Create a 3rd ZFS promoted saved point from the rebuilt container.
    lxc copy c c-save3 -d root,initial.zfs.promote=true
    # > Check that the c container has a ZFS origin of c-save3, which takes c's place.
    has_origin c c-save3
    has_origin c-save3 c-save1
    has_origin c-backup c-save2
    has_origin c-restore2 c-save2
    has_origin c-restore1 c-save1
    has_origin c-save2 c-save1
    has_origin c-save1 image

    # > Let's imagine something went wrong creating the 4th save point and we need to restore from the backup.
    lxc stop --force c
    lxc copy --refresh -c "volatile.uuid=${cUUID}" -d root,initial.zfs.promote=true c-backup c
    [ "$(lxc config get c volatile.uuid)" = "${cUUID}" ]
    has_origin c c-save2
    has_origin c-save3 c-save1
    has_origin c-backup c
    has_origin c-restore2 c-save2
    has_origin c-restore1 c-save1
    has_origin c-save2 c-save1
    has_origin c-save1 image
    lxc start c
    lxc exec c -- test -f /root/testfile1
    lxc exec c -- test -f /root/testfile2
    lxc delete c-backup
    [ "$(zfs list -H -r -t all -o name "${pool}/deleted/containers" | wc -l)" -eq 1 ]

    # > Try again from scratch.
    lxc stop --force c
    lxc copy c c-backup -d root,initial.zfs.promote=true
    lxc rebuild "${image}" c
    [ "$(lxc config get c volatile.uuid)" = "${cUUID}" ]
    has_origin c image
    has_origin c-backup c-save2
    has_origin c-save3 c-save1
    has_origin c-restore2 c-save2
    has_origin c-restore1 c-save1
    has_origin c-save2 c-save1
    has_origin c-save1 image
    lxc start c
    lxc exec c -- test ! -f /root/testfile1
    lxc exec c -- test ! -f /root/testfile2

    # > Create a new version of the c-save1 saved point from the rebuilt container.
    lxc copy c c-save1b -d root,initial.zfs.promote=true
    has_origin c c-save1b
    has_origin c-save1b image
    has_origin c-backup c-save2
    has_origin c-save3 c-save1
    has_origin c-restore2 c-save2
    has_origin c-restore1 c-save1
    has_origin c-save2 c-save1
    has_origin c-save1 image

    # > Assuming everything went well, we can remove the backup.
    lxc delete c-backup
    [ "$(zfs list -H -r -t all -o name "${pool}/deleted/containers" | wc -l)" -eq 1 ]

    # > At some point we can remove the cloned containers.
    lxc delete c-restore1 c-restore2
    [ "$(zfs list -H -r -t all -o name "${pool}/deleted/containers" | wc -l)" -eq 1 ]

    # > At some later stage we'll remove unused save points.
    lxc delete c-save1 c-save2 c-save3
    [ "$(zfs list -H -r -t all -o name "${pool}/deleted/containers" | wc -l)" -eq 1 ]

    # > Eventually we remove the container.
    lxc delete -f c
    [ "$(zfs list -H -r -t all -o name "${pool}/deleted/containers" | wc -l)" -eq 1 ]

    # > Then remove the last save point.
    lxc delete c-save1b
    [ "$(zfs list -H -r -t all -o name "${pool}/deleted/containers" | wc -l)" -eq 1 ]

    # > Check only the pool level ZFS datasets and snapshots remain after deleting containers.
    [ "$(zfs list -H -r -t all -o name "${pool}" | wc -l)" -eq 12 ]
  fi

  # Cleanup
  lxc profile delete "${profile}"

  if [ "$lxd_backend" = "zfs" ] || [ "$lxd_backend" = "lvm" ]; then
    lxc storage delete "${pool}"
  fi
}
