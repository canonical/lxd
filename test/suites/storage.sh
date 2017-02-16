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
      lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm source="${loop_device_3}"
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
    fi

    lxc init testimage c9pool5 -s "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc list -c b c9pool5 | grep "lxdtest-$(basename "${LXD_DIR}")-pool5"

    lxc launch testimage c11pool5 -s "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc list -c b c11pool5 | grep "lxdtest-$(basename "${LXD_DIR}")-pool5"

    if which lvdisplay >/dev/null 2>&1; then
      lxc init testimage c10pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
      lxc list -c b c10pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"

      lxc launch testimage c12pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
      lxc list -c b c12pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"
    fi

    if which zfs >/dev/null 2>&1; then
      lxc launch testimage c13pool7 -s "lxdtest-$(basename "${LXD_DIR}")-pool7"
      lxc launch testimage c14pool7 -s "lxdtest-$(basename "${LXD_DIR}")-pool7"

      lxc launch testimage c15pool8 -s "lxdtest-$(basename "${LXD_DIR}")-pool8"
      lxc launch testimage c16pool8 -s "lxdtest-$(basename "${LXD_DIR}")-pool8"

      lxc launch testimage c17pool9 -s "lxdtest-$(basename "${LXD_DIR}")-pool9"
      lxc launch testimage c18pool9 -s "lxdtest-$(basename "${LXD_DIR}")-pool9"
    fi

    if which zfs >/dev/null 2>&1; then
      lxc delete -f c1pool1
      lxc delete -f c3pool1

      lxc delete -f c4pool2
      lxc delete -f c2pool2
    fi

    if which btrfs >/dev/null 2>&1; then
      lxc delete -f c5pool3
      lxc delete -f c7pool3

      lxc delete -f c8pool4
      lxc delete -f c6pool4
    fi

    lxc delete -f c9pool5
    lxc delete -f c11pool5

    if which lvdisplay >/dev/null 2>&1; then
      lxc delete -f c10pool6
      lxc delete -f c12pool6
    fi

    if which zfs >/dev/null 2>&1; then
      lxc delete -f c13pool7
      lxc delete -f c14pool7

      lxc delete -f c15pool8
      lxc delete -f c16pool8

      lxc delete -f c17pool9
      lxc delete -f c18pool9
    fi

    lxc image delete testimage

    if which zfs >/dev/null 2>&1; then
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool7"
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool8"
      lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool9"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_4}" "${loop_device_4}"

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
    fi
  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
