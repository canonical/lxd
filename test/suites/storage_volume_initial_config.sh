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
  lxc config device set c root initial.block.filesystem=btrfs                  # OK:  No change.
  lxc config device unset c root initial.block.filesystem                      # OK:  Remove existing configuration.
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

    # > Verify initial.zfs.promote functionality.
    lxc launch "${image}" c --no-profiles --storage "${pool}"

    # > Check that container's origin is the base image snapshot.
    [ "$(zfs get origin "${pool}/containers/c" -H -o value)" = "${pool}/images/$(lxc config get c volatile.base_image)@readonly" ]

    # > Create a clone of the container for as a backup for restore later. Ensure a new volatile.uuid is generated.
    lxc copy c c-backup --refresh -c "volatile.uuid=$(uuidgen)" -d root,initial.zfs.promote=true
    [ "$(lxc config get c volatile.uuid)" != "$(lxc config get c-backup volatile.uuid)" ]

    # > Check that the c container has a ZFS origin of c-backup copy snapshot prefix.
    zfs get origin "${pool}/containers/c" -H -o value | grep "^${pool}/containers/c-backup@copy-"

    # > Create a snapshot of the container to check that after promotion is not allowed with snapshots present.
    lxc snapshot c snap0
    ! lxc copy c c-save1 --refresh -c volatile.uuid= -d root,initial.zfs.promote=true || false
    lxc delete c/snap0

    # > Add some data to the container and create a ZFS promoted saved point with a new volatile.uuid.
    lxc exec c -- touch /root/testfile1
    lxc copy c c-save1 --refresh -c "volatile.uuid=$(uuidgen)" -d root,initial.zfs.promote=true
    [ "$(lxc config get c volatile.uuid)" != "$(lxc config get c-save1 volatile.uuid)" ]

    # > Check that the c container has a ZFS origin of c-save1 copy snapshot prefix.
    zfs get origin "${pool}/containers/c" -H -o value | grep "^${pool}/containers/c-save1@copy-"

    # > Add some more data to the container and create a 2nd ZFS promoted saved point with a new volatile.uuid.
    lxc exec c -- touch /root/testfile2
    lxc copy c c-save2 --refresh -c "volatile.uuid=$(uuidgen)" -d root,initial.zfs.promote=true
    [ "$(lxc config get c volatile.uuid)" != "$(lxc config get c-save2 volatile.uuid)" ]

    # > Check that the c container has a ZFS origin of c-save2 copy snapshot prefix.
    zfs get origin "${pool}/containers/c" -H -o value | grep "^${pool}/containers/c-save2@copy-"

    # > Now create new container from the save points and check their data is present.
    lxc copy c-save1 c-restore1
    zfs get origin "${pool}/containers/c-restore1" -H -o value | grep "^${pool}/containers/c-save1@copy-"
    [ "$(lxc config get c-save1  volatile.uuid)" != "$(lxc config get c-restore1 volatile.uuid)" ]
    lxc start c-restore1
    lxc exec c-restore1 -- test -f /root/testfile1
    ! lxc exec c-restore1 -- test -f /root/testfile2 || false
    lxc delete -f c-restore1

    lxc copy c-save2 c-restore2
    zfs get origin "${pool}/containers/c-restore2" -H -o value | grep "^${pool}/containers/c-save2@copy-"
    [ "$(lxc config get c-save2  volatile.uuid)" != "$(lxc config get c-restore2 volatile.uuid)" ]
    lxc start c-restore2
    lxc exec c-restore2 -- test -f /root/testfile1
    lxc exec c-restore2 -- test -f /root/testfile2
    lxc delete -f c-restore2

    # > Lets imagine something went wrong creating the 3rd save point and we need to restore from the backup (keeping the current volatile.uuid).
    lxc stop -f c
    cUUID=$(lxc config get c volatile.uuid)
    lxc rebuild "${image}" c

    # > Create a 3rd ZFS promoted saved point from the rebuilt container.
    lxc copy c c-save3 --refresh -c "volatile.uuid=$(uuidgen)" -d root,initial.zfs.promote=true
    [ "$(lxc config get c volatile.uuid)" != "$(lxc config get c-save3 volatile.uuid)" ]

    # > Check that the c container has a ZFS origin of c-save3 copy snapshot prefix.
    zfs get origin "${pool}/containers/c" -H -o value | grep "^${pool}/containers/c-save3@copy-"

    # > Now restore from backup.
    lxc copy c-backup c --refresh -c "volatile.uuid=${cUUID}" -d root,initial.zfs.promote=true
    lxc delete c-backup

    # > Check that container's origin is the base image snapshot.
    [ "$(zfs get origin "${pool}/containers/c" -H -o value)" = "${pool}/images/$(lxc config get c volatile.base_image)@readonly" ]

    # > Cleanup created containers.
    lxc delete c c-save1 c-save2 c-save3

    # > Chreck only the pool level ZFS datasets remain after deleting containers.
    [ "$(zfs list -H -o name | grep -c "^${pool}")" = "13" ]
  fi

  # Cleanup
  lxc profile delete "${profile}"

  if [ "$lxd_backend" = "zfs" ] || [ "$lxd_backend" = "lvm" ]; then
    lxc storage delete "${pool}"
  fi
}
