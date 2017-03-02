#!/bin/sh

test_storage() {
  # shellcheck disable=2039

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false
  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # shellcheck disable=SC1009
    if which zfs >/dev/null 2>&1; then
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
      zpool create "lxdtest-$(basename "${LXD_DIR}")-pool9-existing-pool" "${loop_device_4}" -f -m none -O compression=on
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool9" zfs source="lxdtest-$(basename "${LXD_DIR}")-pool9-existing-pool"

      # Let LXD create a new dataset and use as pool.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool8" zfs source="lxdtest-$(basename "${LXD_DIR}")-pool1/non-existing-dataset-as-pool"

      # Create device backed zfs pool
      configure_loop_device loop_file_1 loop_device_1
      # shellcheck disable=SC2154
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" zfs source="${loop_device_1}"
    fi

    if which btrfs >/dev/null 2>&1; then
      # Create loop file btrfs pool.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool3" btrfs

      # Create device backed btrfs pool.
      configure_loop_device loop_file_2 loop_device_2
      # shellcheck disable=SC2154
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool4" btrfs source="${loop_device_2}"
    fi

    # Create dir pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool5" dir

    if which lvdisplay >/dev/null 2>&1; then
      # Create lvm pool.
      configure_lvm_loop_device loop_file_3 loop_device_3
      # shellcheck disable=SC2154
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm source="${loop_device_3}" volume.size=25MB

      configure_lvm_loop_device loop_file_5 loop_device_5
      # shellcheck disable=SC2154
      pvcreate "${loop_device_5}"
      # Should fail if vg does not exist, since we have no way of knowing where
      # to create the vg without a block device path set.
      ! lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool10" lvm source=dummy_vg_1 volume.size=25MB

      configure_lvm_loop_device loop_file_6 loop_device_6
      # shellcheck disable=SC2154
      pvcreate "${loop_device_6}"
      vgcreate "lxdtest-$(basename "${LXD_DIR}")-pool11-dummy_vg_2" "${loop_device_6}"
      # Reuse existing volume group "dummy_vg_2" on existing physical volume.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool11" lvm source="lxdtest-$(basename "${LXD_DIR}")-pool11-dummy_vg_2" volume.size=25MB

      configure_lvm_loop_device loop_file_7 loop_device_7
      # shellcheck disable=SC2154
      pvcreate "${loop_device_7}"
      vgcreate "lxdtest-$(basename "${LXD_DIR}")-pool12-dummy_vg_3" "${loop_device_7}"
      # Reuse existing volume group "dummy_vg_3" on existing physical volume.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool12" lvm source="${loop_device_7}" lvm.vg_name="lxdtest-$(basename "${LXD_DIR}")-pool12-dummy_vg_3" volume.size=25MB

      configure_lvm_loop_device loop_file_8 loop_device_8
      # shellcheck disable=SC2154
      pvcreate "${loop_device_8}"
      # Create new volume group "dummy_vg_4" on existing physical volume.
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool13" lvm source="${loop_device_8}" lvm.vg_name="lxdtest-$(basename "${LXD_DIR}")-pool13-dummy_vg_4" volume.size=25MB

      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool14" lvm volume.size=25MB
    fi

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool5"

    # Import image into default storage pool.
    ensure_import_testimage

    # Muck around with some containers on various pools.
    if which zfs >/dev/null 2>&1; then
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

    if which btrfs >/dev/null 2>&1; then
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

    if which lvdisplay >/dev/null 2>&1; then
      lxc init testimage c10pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
      lxc list -c b c10pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"

      # Test if volume group renaming works by setting lvm.vg_name.
      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm.vg_name "lxdtest-$(basename "${LXD_DIR}")-pool6-newName" 

      lxc storage set "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm.thinpool_name "lxdtest-$(basename "${LXD_DIR}")-pool6-newThinpoolName" 

      lxc launch testimage c12pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
      lxc list -c b c12pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"

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
    fi

    if which zfs >/dev/null 2>&1; then
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
    fi

    if which zfs >/dev/null 2>&1; then
      lxc delete -f c1pool1
      lxc delete -f c3pool1

      lxc delete -f c4pool2
      lxc delete -f c2pool2

      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c1pool1
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool1" c2pool2
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c3pool1
      lxc storage volume delete "lxdtest-$(basename "${LXD_DIR}")-pool2" c4pool2
    fi

    if which btrfs >/dev/null 2>&1; then
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

    if which lvdisplay >/dev/null 2>&1; then
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
    fi

    if which zfs >/dev/null 2>&1; then
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

    if which zfs >/dev/null 2>&1; then
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

    if which btrfs >/dev/null 2>&1; then
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool4"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_2}" "${loop_device_2}"
    fi

    if which lvdisplay >/dev/null 2>&1; then
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool6"
      # shellcheck disable=SC2154
      deconfigure_lvm_loop_device "${loop_file_3}" "${loop_device_3}"

      # shellcheck disable=SC2154
      deconfigure_lvm_loop_device "${loop_file_5}" "${loop_device_5}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool11"
      # shellcheck disable=SC2154
      deconfigure_lvm_loop_device "${loop_file_6}" "${loop_device_6}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool12"
      # shellcheck disable=SC2154
      deconfigure_lvm_loop_device "${loop_file_7}" "${loop_device_7}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool13"
      # shellcheck disable=SC2154
      deconfigure_lvm_loop_device "${loop_file_8}" "${loop_device_8}"

      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool14"
    fi
  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
