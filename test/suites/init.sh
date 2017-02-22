#!/bin/sh

test_lxd_autoinit() {
  # - lxd init --auto --storage-backend zfs
  # and
  # - lxd init --auto
  # can't be easily tested on jenkins since it hard-codes "default" as pool
  # naming. This can cause naming conflicts when multiple test-suites are run on
  # a single runner.

  # lxd init --auto --storage-backend zfs --storage-pool <name>
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    configure_loop_device loop_file_1 loop_device_1
    # shellcheck disable=SC2154

    zpool create "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool" "${loop_device_1}" -m none -O compression=on
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool"

    kill_lxd "${LXD_INIT_DIR}"
  fi

  # lxd init --auto --storage-backend zfs --storage-pool <name>/<non-existing-dataset>
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    configure_loop_device loop_file_1 loop_device_1
    # shellcheck disable=SC2154

    zpool create "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool" "${loop_device_1}" -m none -O compression=on
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/non-existing-dataset"

    kill_lxd "${LXD_INIT_DIR}"
  fi

  # lxd init --auto --storage-backend zfs --storage-pool <name>/<existing-dataset>
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false

    # shellcheck disable=SC2154
    zfs create -p -o mountpoint=none "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/existing-dataset"
    LXD_DIR=${LXD_INIT_DIR} lxd init --auto --storage-backend zfs --storage-pool "lxdtest-$(basename "${LXD_DIR}")-pool1-existing-pool/existing-dataset"

    kill_lxd "${LXD_INIT_DIR}"
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
