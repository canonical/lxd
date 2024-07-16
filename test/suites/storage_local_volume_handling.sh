test_storage_local_volume_handling() {
  ensure_import_testimage

  local LXD_STORAGE_DIR lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false

  ensure_import_testimage

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"
    pool_base="lxdtest-$(basename "${LXD_DIR}")"

    if storage_backend_available "btrfs"; then
      lxc storage create "${pool_base}-btrfs" btrfs size=1GiB
    fi

    if storage_backend_available "ceph"; then
      lxc storage create "${pool_base}-ceph" ceph volume.size=25MiB ceph.osd.pg_num=16
      if [ -n "${LXD_CEPH_CEPHFS:-}" ]; then
        lxc storage create "${pool_base}-cephfs" cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")-cephfs"
      fi
    fi

    lxc storage create "${pool_base}-dir" dir

    if storage_backend_available "lvm"; then
      lxc storage create "${pool_base}-lvm" lvm volume.size=25MiB
    fi

    if storage_backend_available "zfs"; then
      lxc storage create "${pool_base}-zfs" zfs size=1GiB
    fi

    # Test all combinations of our storage drivers

    driver="${lxd_backend}"
    pool="${pool_base}-${driver}"
    project="${pool_base}-project"
    pool_opts=

    if [ "$driver" = "btrfs" ] || [ "$driver" = "zfs" ]; then
      pool_opts="size=1GiB"
    fi

    if [ "$driver" = "ceph" ]; then
      pool_opts="volume.size=25MiB ceph.osd.pg_num=16"
    fi

    if [ "$driver" = "lvm" ]; then
      pool_opts="volume.size=25MiB"
    fi

    if [ -n "${pool_opts}" ]; then
      # shellcheck disable=SC2086
      lxc storage create "${pool}1" "${driver}" $pool_opts
    else
      lxc storage create "${pool}1" "${driver}"
    fi

    lxc storage volume create "${pool}" vol1
    lxc storage volume set "${pool}" vol1 user.foo=snap0
    lxc storage volume set "${pool}" vol1 snapshots.expiry=1H

    # This will create the snapshot vol1/snap0
    lxc storage volume snapshot "${pool}" vol1

    # This will create the snapshot vol1/snap1
    lxc storage volume set "${pool}" vol1 user.foo=snap1
    lxc storage volume snapshot "${pool}" vol1
    lxc storage volume set "${pool}" vol1 user.foo=postsnap1

    # Copy volume with snapshots in same pool
    lxc storage volume copy "${pool}/vol1" "${pool}/vol1copy"

    # Ensure the target snapshots are there
    lxc storage volume show "${pool}" vol1copy/snap0
    lxc storage volume show "${pool}" vol1copy/snap1

    # Check snapshot volume config was copied
    [ "$(lxc storage volume get "${pool}" vol1copy user.foo)" = "postsnap1" ]
    [ "$(lxc storage volume get "${pool}" vol1copy/snap0 user.foo)" = "snap0" ]
    [ "$(lxc storage volume get "${pool}" vol1copy/snap1 user.foo)" = "snap1" ]

    # Check the volume and snapshot UUIDs are different
    [ "$(lxc storage volume get "${pool}" vol1 volatile.uuid)" != "$(lxc storage volume get "${pool}" vol1copy volatile.uuid)" ]
    [ "$(lxc storage volume get "${pool}" vol1/snap0 volatile.uuid)" != "$(lxc storage volume get "${pool}" vol1copy/snap0 volatile.uuid)" ]
    [ "$(lxc storage volume get "${pool}" vol1/snap1 volatile.uuid)" != "$(lxc storage volume get "${pool}" vol1copy/snap1 volatile.uuid)" ]
    lxc storage volume delete "${pool}" vol1copy

    # Copy volume with snapshots in different pool
    lxc storage volume copy "${pool}/vol1" "${pool}1/vol1"

    # Ensure the target snapshots are there
    lxc storage volume show "${pool}1" vol1/snap0
    lxc storage volume show "${pool}1" vol1/snap1

    # Check snapshot volume config was copied
    [ "$(lxc storage volume get "${pool}1" vol1 user.foo)" = "postsnap1" ]
    [ "$(lxc storage volume get "${pool}1" vol1/snap0 user.foo)" = "snap0" ]
    [ "$(lxc storage volume get "${pool}1" vol1/snap1 user.foo)" = "snap1" ]

    # Copy volume only
    lxc storage volume copy --volume-only "${pool}/vol1" "${pool}1/vol2"

    # Ensure the target snapshots are not there
    ! lxc storage volume show "${pool}1" vol2/snap0 || false
    ! lxc storage volume show "${pool}1" vol2/snap1 || false

    # Check snapshot volume config was copied
    [ "$(lxc storage volume get "${pool}1" vol2 user.foo)" = "postsnap1" ]

    # Copy snapshot to volume
    lxc storage volume copy "${pool}/vol1/snap0" "${pool}1/vol3"

    # Check snapshot volume config was copied from snapshot
    lxc storage volume show "${pool}1" vol3
    [ "$(lxc storage volume get "${pool}1" vol3 user.foo)" = "snap0" ]

    # Rename custom volume using `lxc storage volume move`
    lxc storage volume move "${pool}1/vol1" "${pool}1/vol4"
    lxc storage volume move "${pool}1/vol4" "${pool}1/vol1"

    # Move volume between projects
    lxc project create "${project}"
    lxc storage volume move "${pool}1/vol1" "${pool}1/vol1" --project default --target-project "${project}"
    lxc storage volume show "${pool}1" vol1 --project "${project}"
    lxc storage volume move "${pool}1/vol1" "${pool}1/vol1" --project "${project}" --target-project default
    lxc storage volume show "${pool}1" vol1 --project default

    # Create new pools
    lxc storage create pool_1 dir
    lxc storage create pool_2 dir

    # Create volumes with same name on another pool
    lxc storage volume create pool_1 vol1
    lxc storage volume create pool_2 vol1
    lxc storage volume create pool_1 vol2
    lxc storage volume create pool_2 vol2

    # List volumes from all pools
    lxc storage volume list --format csv --columns pn | grep "pool_1,vol1"
    lxc storage volume list --format csv --columns pn | grep "pool_2,vol1"
    lxc storage volume list --format csv --columns pn | grep "pool_1,vol2"
    lxc storage volume list --format csv --columns pn | grep "pool_2,vol2"

    lxc storage volume delete pool_1 vol1
    lxc storage volume delete pool_1 vol2
    lxc storage delete pool_1
    lxc storage volume delete pool_2 vol1
    lxc storage volume delete pool_2 vol2
    lxc storage delete pool_2
    lxc project delete "${project}"
    lxc storage volume delete "${pool}1" vol1
    lxc storage volume delete "${pool}1" vol2
    lxc storage volume delete "${pool}1" vol3
    lxc storage volume move "${pool}/vol1" "${pool}1/vol1"
    ! lxc storage volume show "${pool}" vol1 || false
    lxc storage volume show "${pool}1" vol1
    lxc storage volume delete "${pool}1" vol1
    lxc storage delete "${pool}1"

    for source_driver in "btrfs" "ceph" "cephfs" "dir" "lvm" "zfs"; do
      for target_driver in "btrfs" "ceph" "cephfs" "dir" "lvm" "zfs"; do
        # shellcheck disable=SC2235
        if [ "$source_driver" != "$target_driver" ] \
            && ([ "$lxd_backend" = "$source_driver" ] || ([ "$lxd_backend" = "ceph" ] && [ "$source_driver" = "cephfs" ] && [ -n "${LXD_CEPH_CEPHFS:-}" ])) \
            && storage_backend_available "$source_driver" && storage_backend_available "$target_driver"; then
          source_pool="${pool_base}-${source_driver}"
          target_pool="${pool_base}-${target_driver}"

          # source_driver -> target_driver
          lxc storage volume create "${source_pool}" vol1
          # This will create the snapshot vol1/snap0
          lxc storage volume snapshot "${source_pool}" vol1
          # Copy volume with snapshots
          lxc storage volume copy "${source_pool}/vol1" "${target_pool}/vol1"
          # Ensure the target snapshot is there
          lxc storage volume show "${target_pool}" vol1/snap0
          # Copy volume only
          lxc storage volume copy --volume-only "${source_pool}/vol1" "${target_pool}/vol2"
          # Copy snapshot to volume
          lxc storage volume copy "${source_pool}/vol1/snap0" "${target_pool}/vol3"
          lxc storage volume delete "${target_pool}" vol1
          lxc storage volume delete "${target_pool}" vol2
          lxc storage volume delete "${target_pool}" vol3
          lxc storage volume move "${source_pool}/vol1" "${target_pool}/vol1"
          ! lxc storage volume show "${source_pool}" vol1 || false
          lxc storage volume show "${target_pool}" vol1
          lxc storage volume delete "${target_pool}" vol1

          # target_driver -> source_driver
          lxc storage volume create "${target_pool}" vol1
          lxc storage volume copy "${target_pool}/vol1" "${source_pool}/vol1"
          lxc storage volume delete "${source_pool}" vol1

          lxc storage volume move "${target_pool}/vol1" "${source_pool}/vol1"
          ! lxc storage volume show "${target_pool}" vol1 || false
          lxc storage volume show "${source_pool}" vol1
          lxc storage volume delete "${source_pool}" vol1

          if [ "${source_driver}" = "cephfs" ] || [ "${target_driver}" = "cephfs" ]; then
            continue
          fi

          # create custom block volume without snapshots
          lxc storage volume create "${source_pool}" vol1 --type=block size=4194304
          lxc storage volume copy "${source_pool}/vol1" "${target_pool}/vol1"
          lxc storage volume show "${target_pool}" vol1 | grep -q 'content_type: block'

          # create custom block volume with a snapshot
          lxc storage volume create "${source_pool}" vol2 --type=block size=4194304
          lxc storage volume snapshot "${source_pool}" vol2
          lxc storage volume show "${source_pool}" vol2/snap0 | grep -q 'content_type: block'

          # restore snapshot
          lxc storage volume restore "${source_pool}" vol2 snap0
          lxc storage volume show "${source_pool}" vol2 | grep -q 'content_type: block'

          # copy with snapshots
          lxc storage volume copy "${source_pool}/vol2" "${target_pool}/vol2"
          lxc storage volume show "${target_pool}" vol2 | grep -q 'content_type: block'
          lxc storage volume show "${target_pool}" vol2/snap0 | grep -q 'content_type: block'

          # copy without snapshots
          lxc storage volume copy "${source_pool}/vol2" "${target_pool}/vol3" --volume-only
          lxc storage volume show "${target_pool}" vol3 | grep -q 'content_type: block'
          ! lxc storage volume show "${target_pool}" vol3/snap0 | grep -q 'content_type: block' || false

          # move images
          lxc storage volume move "${source_pool}/vol2" "${target_pool}/vol4"
          ! lxc storage volume show "${source_pool}" vol2 | grep -q 'content_type: block' || false
          lxc storage volume show "${target_pool}" vol4 | grep -q 'content_type: block'
          lxc storage volume show "${target_pool}" vol4/snap0 | grep -q 'content_type: block'

          # check refreshing volumes

          # create storage volume with user config differing over snapshots
          lxc storage volume create "${source_pool}" vol5 --type=block size=4194304
          lxc storage volume set "${source_pool}" vol5 user.foo=snap0vol5
          lxc storage volume snapshot "${source_pool}" vol5
          lxc storage volume set "${source_pool}" vol5 user.foo=snap1vol5
          lxc storage volume snapshot "${source_pool}" vol5
          lxc storage volume set "${source_pool}" vol5 user.foo=snapremovevol5
          lxc storage volume snapshot "${source_pool}" vol5 snapremove
          lxc storage volume set "${source_pool}" vol5 user.foo=postsnap1vol5

          # create storage volume with user config differing over snapshots and additional snapshot than vol5
          lxc storage volume create "${source_pool}" vol6 --type=block size=4194304
          lxc storage volume set "${source_pool}" vol6 user.foo=snap0vol6
          lxc storage volume snapshot "${source_pool}" vol6
          lxc storage volume set "${source_pool}" vol6 user.foo=snap1vol6
          lxc storage volume snapshot "${source_pool}" vol6
          lxc storage volume set "${source_pool}" vol6 user.foo=snap2vol6
          lxc storage volume snapshot "${source_pool}" vol6
          lxc storage volume set "${source_pool}" vol6 user.foo=postsnap1vol6

          # copy to new volume destination with refresh flag
          lxc storage volume copy --refresh "${source_pool}/vol5" "${target_pool}/vol5"

          # check snapshot volumes (including config) were copied
          [ "$(lxc storage volume get "${target_pool}" vol5 user.foo)" = "postsnap1vol5" ]
          [ "$(lxc storage volume get "${target_pool}" vol5/snap0 user.foo)" = "snap0vol5" ]
          [ "$(lxc storage volume get "${target_pool}" vol5/snap1 user.foo)" = "snap1vol5" ]
          [ "$(lxc storage volume get "${target_pool}" vol5/snapremove user.foo)" = "snapremovevol5" ]

          # incremental copy to existing volume destination with refresh flag
          lxc storage volume copy --refresh "${source_pool}/vol6" "${target_pool}/vol5"

          # check snapshot volumes (including config) was overridden from new source and that missing snapshot is
          # present and that the missing snapshot has been removed.
          # Note: Due to a known issue we are currently only diffing the snapshots by name and creation date, so infact existing
          # snapshots of the same name and date won't be overwritten even if their config or contents is different.
          [ "$(lxc storage volume get "${target_pool}" vol5 user.foo)" = "postsnap1vol5" ]
          [ "$(lxc storage volume get "${target_pool}" vol5/snap0 user.foo)" = "snap0vol6" ]
          [ "$(lxc storage volume get "${target_pool}" vol5/snap1 user.foo)" = "snap1vol6" ]
          [ "$(lxc storage volume get "${target_pool}" vol5/snap2 user.foo)" = "snap2vol6" ]
          ! lxc storage volume get "${target_pool}" vol5/snapremove user.foo || false

          # check that another refresh doesn't change the volume's and snapshot's UUID
          old_uuid="$(lxc storage volume get "${target_pool}" vol5 volatile.uuid)"
          old_snap0_uuid="$(lxc storage volume get "${target_pool}" vol5/snap0 volatile.uuid)"
          lxc storage volume copy --refresh "${source_pool}/vol6" "${target_pool}/vol5"
          [ "$(lxc storage volume get "${target_pool}" vol5 volatile.uuid)" = "${old_uuid}" ]
          [ "$(lxc storage volume get "${target_pool}" vol5/snap0 volatile.uuid)" = "${old_snap0_uuid}" ]

          # copy ISO custom volumes
          truncate -s 25MiB foo.iso
          lxc storage volume import "${source_pool}" ./foo.iso iso1
          lxc storage volume copy "${source_pool}/iso1" "${target_pool}/iso1"
          lxc storage volume show "${target_pool}" iso1 | grep -q 'content_type: iso'
          lxc storage volume move "${source_pool}/iso1" "${target_pool}/iso2"
          lxc storage volume show "${target_pool}" iso2 | grep -q 'content_type: iso'
          ! lxc storage volume show "${source_pool}" iso1 || false

          # clean up
          lxc storage volume delete "${source_pool}" vol1
          lxc storage volume delete "${target_pool}" vol1
          lxc storage volume delete "${target_pool}" vol2
          lxc storage volume delete "${target_pool}" vol3
          lxc storage volume delete "${target_pool}" vol4
          lxc storage volume delete "${source_pool}" vol5
          lxc storage volume delete "${target_pool}" vol5
          lxc storage volume delete "${source_pool}" vol6
          lxc storage volume delete "${target_pool}" iso1
          lxc storage volume delete "${target_pool}" iso2
          rm -f foo.iso
        fi
      done
    done
  )

  # shellcheck disable=SC2031,2269
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
