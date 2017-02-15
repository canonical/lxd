#!/bin/sh

configure_lvm_loop_device() {
  lv_loop_file=$(mktemp -p "${TEST_DIR}" XXXX.lvm)
  truncate -s 4G "${lv_loop_file}"
  pvloopdev=$(losetup --show -f "${lv_loop_file}")
  if [ ! -e "${pvloopdev}" ]; then
    echo "failed to setup loop"
    false
  fi

  pvcreate "${pvloopdev}"

  # The following code enables to return a value from a shell function by
  # calling the function as: fun VAR1

  # shellcheck disable=2039
  local  __tmp1="${1}"
  # shellcheck disable=2039
  local  res1="${lv_loop_file}"
  if [ "${__tmp1}" ]; then
      eval "${__tmp1}='${res1}'"
  fi

  # shellcheck disable=2039
  local  __tmp2="${2}"
  # shellcheck disable=2039
  local  res2="${pvloopdev}"
  if [ "${__tmp2}" ]; then
      eval "${__tmp2}='${res2}'"
  fi
}

deconfigure_lvm_loop_device() {
  lv_loop_file="${1}"
  loopdev="${2}"

  SUCCESS=0
  # shellcheck disable=SC2034
  for i in $(seq 10); do
    pvremove -f "${loopdev}" > /dev/null 2>&1 || true
    if losetup -d "${loopdev}"; then
      SUCCESS=1
      break
    fi

    sleep 0.5
  done

  if [ "${SUCCESS}" = "0" ]; then
    echo "Failed to tear down loop device."
    false
  fi

  rm -f "${lv_loop_file}"
}

configure_loop_device() {
  lv_loop_file=$(mktemp -p "${TEST_DIR}" XXXX.img)
  truncate -s 10G "${lv_loop_file}"
  pvloopdev=$(losetup --show -f "${lv_loop_file}")
  if [ ! -e "${pvloopdev}" ]; then
    echo "failed to setup loop"
    false
  fi

  # The following code enables to return a value from a shell function by
  # calling the function as: fun VAR1

  # shellcheck disable=2039
  local  __tmp1="${1}"
  # shellcheck disable=2039
  local  res1="${lv_loop_file}"
  if [ "${__tmp1}" ]; then
      eval "${__tmp1}='${res1}'"
  fi

  # shellcheck disable=2039
  local  __tmp2="${2}"
  # shellcheck disable=2039
  local  res2="${pvloopdev}"
  if [ "${__tmp2}" ]; then
      eval "${__tmp2}='${res2}'"
  fi
}

deconfigure_loop_device() {
  lv_loop_file="${1}"
  loopdev="${2}"

  SUCCESS=0
  # shellcheck disable=SC2034
  for i in $(seq 10); do
    if losetup -d "${loopdev}"; then
      SUCCESS=1
      break
    fi

    sleep 0.5
  done

  if [ "${SUCCESS}" = "0" ]; then
    echo "Failed to tear down loop device"
    false
  fi

  rm -f "${lv_loop_file}"
}

test_storage() {
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false
  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # Only create zfs pools on 64 bit arches. I think getconf LONG_BIT should
    # even work when running a 32bit userspace on a 64 bit kernel.
    ARCH=$(getconf LONG_BIT)
    BACKEND=btrfs
    if [ "${ARCH}" = "64" ]; then
      BACKEND=zfs
    fi

    # Create loop file zfs pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" "${BACKEND}"

    # Create device backed zfs pool
    configure_loop_device loop_file_1 loop_device_1
    # shellcheck disable=SC2154
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool2" "${BACKEND}" source="${loop_device_1}"

    # Create loop file btrfs pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool3" btrfs

    # Create device backed btrfs pool.
    configure_loop_device loop_file_2 loop_device_2
    # shellcheck disable=SC2154
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool4" btrfs source="${loop_device_2}"

    # Create dir pool.
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool5" dir

    # Create lvm pool.
    configure_lvm_loop_device loop_file_3 loop_device_3
    # shellcheck disable=SC2154
    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool6" lvm source="${loop_device_3}"

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    # Muck around with some containers on various pools.
    lxc init testimage c1pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc list -c b c1pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc init testimage c2pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
    lxc list -c b c2pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

    lxc launch testimage c3pool1 -s "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc list -c b c3pool1 | grep "lxdtest-$(basename "${LXD_DIR}")-pool1"
    lxc launch testimage c4pool2 -s "lxdtest-$(basename "${LXD_DIR}")-pool2"
    lxc list -c b c4pool2 | grep "lxdtest-$(basename "${LXD_DIR}")-pool2"

    lxc init testimage c5pool3 -s "lxdtest-$(basename "${LXD_DIR}")-pool3"
    lxc list -c b c5pool3 | grep "lxdtest-$(basename "${LXD_DIR}")-pool3"
    lxc init testimage c6pool4 -s "lxdtest-$(basename "${LXD_DIR}")-pool4"
    lxc list -c b c6pool4 | grep "lxdtest-$(basename "${LXD_DIR}")-pool4"

    lxc launch testimage c7pool3 -s "lxdtest-$(basename "${LXD_DIR}")-pool3"
    lxc list -c b c7pool3 | grep "lxdtest-$(basename "${LXD_DIR}")-pool3"
    lxc launch testimage c8pool4 -s "lxdtest-$(basename "${LXD_DIR}")-pool4"
    lxc list -c b c8pool4 | grep "lxdtest-$(basename "${LXD_DIR}")-pool4"

    lxc init testimage c9pool5 -s "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc list -c b c9pool5 | grep "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc init testimage c10pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
    lxc list -c b c10pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"

    lxc launch testimage c11pool5 -s "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc list -c b c11pool5 | grep "lxdtest-$(basename "${LXD_DIR}")-pool5"
    lxc launch testimage c12pool6 -s "lxdtest-$(basename "${LXD_DIR}")-pool6"
    lxc list -c b c12pool6 | grep "lxdtest-$(basename "${LXD_DIR}")-pool6"

    lxc delete -f c1pool1
    lxc delete -f c2pool2

    lxc delete -f c3pool1
    lxc delete -f c4pool2

    lxc delete -f c5pool3
    lxc delete -f c6pool4

    lxc delete -f c7pool3
    lxc delete -f c8pool4

    lxc delete -f c9pool5
    lxc delete -f c10pool6

    lxc delete -f c11pool5
    lxc delete -f c12pool6

    lxc image delete testimage

    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool2"
    # shellcheck disable=SC2154
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"

    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool4"
    # shellcheck disable=SC2154
    deconfigure_loop_device "${loop_file_2}" "${loop_device_2}"

    lxc storage delete "lxdtest-$(basename "${LXD_DIR}")-pool6"
    # shellcheck disable=SC2154
    deconfigure_lvm_loop_device "${loop_file_3}" "${loop_device_3}"
  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
