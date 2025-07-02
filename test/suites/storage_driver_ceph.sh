test_storage_driver_ceph() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" != "ceph" ]; then
    echo "==> SKIP: test_storage_driver_ceph only supports 'ceph', not ${lxd_backend}"
    return
  fi

  local LXD_STORAGE_DIR

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # shellcheck disable=SC1009
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" ceph volume.size=25MiB ceph.osd.pg_num=16

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    # create osd pool
    ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool create "lxdtest-$(basename "${LXD_DIR}")-existing-osd-pool" 1

    # Let LXD use an already existing osd pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" ceph source="lxdtest-$(basename "${LXD_DIR}")-existing-osd-pool" volume.size=25MiB ceph.osd.pg_num=16

    # Test that no invalid ceph storage pool configuration keys can be set.
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.thinpool_name=bla || false
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.use_thinpool=false || false
    ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.vg_name=bla || false

    # Test that all valid ceph storage pool configuration keys can be set.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-ceph-pool-config" ceph volume.block.filesystem=ext4 volume.block.mount_options=discard volume.size=25MiB ceph.rbd.clone_copy=true ceph.osd.pg_num=16
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
    lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool1" volume.size 300MiB # modern xfs requires 300MiB or more
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
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 c1pool1 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 c1pool1
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 c1pool1

    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2 c2pool2

    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1 c3pool1

    lxc storage volume create "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2
    lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice /opt
    ! lxc storage volume attach "lxdtest-$(basename "${LXD_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice2 /opt || false
    lxc storage volume detach "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2
    lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2 c4pool2-renamed
    lxc storage volume rename "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2-renamed c4pool2

    lxc delete -f c1pool1
    lxc delete -f c3pool1
    lxc delete -f c5pool1

    lxc delete -f c4pool2
    lxc delete -f c2pool2

    lxc storage volume set "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 size 500MiB
    lxc storage volume unset "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1 size

    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1
    lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2

    lxc image delete testimage
    lxc profile device remove default root
    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool2"
    ! ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool ls | grep -F "lxdtest-$(basename "${LXD_DIR}")-existing-osd-pool" || false


    # Test that pre-existing OSD pools are not affected by the config option in LXD. Only the associated pool should be affected.
    # .mgr is auto-created by Ceph, so give it a value that is different from the size supplied to the `lxc storage create` commands.
    ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool set ".mgr" size 3
    pool1="lxdtest-$(basename "${LXD_DIR}")-pool1"
    pool2="lxdtest-$(basename "${LXD_DIR}")-pool2"
    lxc storage create "${pool1}" ceph volume.size=25MiB ceph.osd.pg_num=16 ceph.osd.pool_size=1
    [[ "$(ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool get "${pool1}" size --format json | jq '.size')" = "1" ]]
    [[ "$(ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool get ".mgr" size --format json | jq '.size')" = "3" ]]

    lxc storage create "${pool2}" ceph volume.size=25MiB ceph.osd.pg_num=16 ceph.osd.pool_size=2
    [[ "$(ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool get "${pool1}" size --format json | jq '.size')" = "1" ]]
    [[ "$(ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool get "${pool2}" size --format json | jq '.size')" = "2" ]]
    [[ "$(ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool get ".mgr" size --format json | jq '.size')" = "3" ]]

    lxc storage delete "${pool1}"
    lxc storage delete "${pool2}"
    ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool set ".mgr" size 1 --yes-i-really-mean-it
  )

  # shellcheck disable=SC2031
  kill_lxd "${LXD_STORAGE_DIR}"
}
