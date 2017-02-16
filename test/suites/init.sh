#!/bin/sh

test_lxd_autoinit() {
  # lxd init --auto
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_INIT_DIR}"
  spawn_lxd "${LXD_INIT_DIR}" false

  ZFS_POOL="lxdtest-$(basename "${LXD_DIR}")-init"
  LXD_DIR=${LXD_INIT_DIR} lxd init --auto

  kill_lxd "${LXD_INIT_DIR}"

  # lxd init --auto --storage-backend zfs
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs

    kill_lxd "${LXD_INIT_DIR}"
  fi

  # lxd init --auto --storage-backend zfs --storage-pool <name>
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    configure_loop_device loop_file_1 loop_device_1
    zpool create "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool" "${loop_device_1}" -m none -O compression=on
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool"

    kill_lxd "${LXD_INIT_DIR}"
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
  fi

  # lxd init --auto --storage-backend zfs --storage-pool <name>/<non-existing-dataset>
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    configure_loop_device loop_file_1 loop_device_1
    zpool create "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool" "${loop_device_1}" -m none -O compression=on
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/non-existing-dataset"

    kill_lxd "${LXD_INIT_DIR}"
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    zpool destroy -f "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool"
  fi

  # lxd init --auto --storage-backend zfs --storage-pool <name>/<existing-dataset>
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    configure_loop_device loop_file_1 loop_device_1
    zpool create "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool" "${loop_device_1}" -f -m none -O compression=on
    zfs create -p -o mountpoint=none "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/existing-dataset"
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/existing-dataset"

    kill_lxd "${LXD_INIT_DIR}"
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    zpool destroy -f "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool"
  fi

 # lxd init --storage-backend zfs --storage-create-loop 1 --storage-pool <name> --auto
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    ZFS_POOL="lxdtest-$(basename "${LXD_DIR}")-init"
    LXD_DIR=${LXD_INIT_DIR} lxd init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

    kill_lxd "${LXD_INIT_DIR}"
  fi
}
