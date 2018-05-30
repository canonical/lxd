test_storage_driver_ceph() {
  ensure_import_testimage

  # shellcheck disable=2039
  local LXD_STORAGE_DIR lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    if [ "$lxd_backend" != "ceph" ]; then
      exit 0
    fi

    # shellcheck disable=SC1009
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" ceph volume.size=25MB ceph.osd.pg_num=1

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    # create osd pool
    ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool create "lxdtest-$(basename "${LXD_DIR}")-existing-osd-pool" 32

    # Let LXD use an already existing osd pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" ceph source="lxdtest-$(basename "${LXD_DIR}")-existing-osd-pool" volume.size=25MB ceph.osd.pg_num=1

    # Test that no invalid ceph storage pool configuration keys can be set.
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.thinpool_name=bla
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.use_thinpool=false
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.vg_name=bla

    # Test that all valid ceph storage pool configuration keys can be set.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-ceph-pool-config" ceph volume.block.filesystem=ext4 volume.block.mount_options=discard volume.size=2GB ceph.rbd.clone_copy=true ceph.osd.pg_num=1
    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-ceph-pool-config"

    # Muck around with some containers on various pools.
    lxc init testimage c1pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc list -c b c1pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"

    lxc init testimage c2pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
    lxc list -c b c2pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

    lxc launch testimage c3pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc list -c b c3pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"

    lxc launch testimage c4pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
    lxc list -c b c4pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

    lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool1" volume.block.filesystem xfs
    # xfs is unhappy with block devices < 50 MB. It seems to calculate the
    # ag{count,size} parameters wrong and/or sets the data area too big.
    lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool1" volume.size 50MB
    lxc init testimage c5pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Test whether dependency tracking is working correctly. We should be able
    # to create a container, copy it, which leads to a dependency relation
    # between the source container's storage volume and the copied container's
    # storage volume. Now, we delete the source container which will trigger a
    # rename operation and not an actual delete operation. Now we create a
    # container of the same name as the source container again, create a copy of
    # it to introduce another dependency relation. Now we delete the source
    # container again. This should work. If it doesn't it means the rename
    # operation tries to map the two source to the same name.
    lxc init testimage a -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc copy a b
    lxc delete a
    lxc init testimage a -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc copy a c
    lxc delete a
    lxc delete b
    lxc delete c

    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1
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

    lxc delete -f c1pool1
    lxc delete -f c3pool1
    lxc delete -f c5pool1

    lxc delete -f c4pool2
    lxc delete -f c2pool2

    lxc storage volume set "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 size 500MB
    lxc storage volume unset "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 size

    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2

    lxc image delete testimage
    lxc profile device remove default root
    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool2"

  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
