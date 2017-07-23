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

    # shellcheck disable=SC1009
    if [ "$lxd_backend" = "ceph" ]; then
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" ceph

      # Set default storage pool for image import.
      lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

      # Import image into default storage pool.
      ensure_import_testimage

      # create osd pool
      ceph --cluster "${LXD_CEPH_CLUSTER}" osd pool create "lxdtest-$(basename "${LXD_DIR}")-existing-osd-pool" 32

      # Let LXD use an already existing osd pool.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" ceph source="lxdtest-$(basename "${LXD_DIR}")-existing-osd-pool"

      # Test that no invalid ceph storage pool configuration keys can be set.
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.thinpool_name=bla
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.use_thinpool=false
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-invalid-ceph-pool-config" ceph lvm.vg_name=bla

      # Test that all valid ceph storage pool configuration keys can be set.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-valid-ceph-pool-config" ceph volume.block.filesystem=ext4 volume.block.mount_options=discard volume.size=2GB ceph.rbd.clone_copy=true ceph.osd.pg_num=32
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-valid-ceph-pool-config"
    fi

    # Muck around with some containers on various pools.
    if [ "$lxd_backend" = "ceph" ]; then
      lxc init testimage c1pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
      lxc list -c b c1pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"

      lxc init testimage c2pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
      lxc list -c b c2pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

      lxc launch testimage c3pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
      lxc list -c b c3pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"

      lxc launch testimage c4pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
      lxc list -c b c4pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

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
    fi

    if [ "$lxd_backend" = "ceph" ]; then
      lxc delete -f c1pool1
      lxc delete -f c3pool1

      lxc delete -f c4pool2
      lxc delete -f c2pool2

      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2
    fi

    if [ "$lxd_backend" = "ceph" ]; then
      lxc image delete testimage
      lxc profile device remove default root
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool1"
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool2"
    fi

  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
