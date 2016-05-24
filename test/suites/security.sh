#!/bin/sh

test_security() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # CVE-2016-1581
  if [ "${LXD_BACKEND}" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}"

    ZFS_POOL="lxdtest-$(basename "${LXD_DIR}")-init"
    LXD_DIR=${LXD_INIT_DIR} lxd init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

    PERM=$(stat -c %a "${LXD_INIT_DIR}/zfs.img")
    if [ "${PERM}" != "600" ]; then
      echo "Bad zfs.img permissions: ${PERM}"
      zpool destroy "${ZFS_POOL}"
      false
    fi

    zpool destroy "${ZFS_POOL}"
    kill_lxd "${LXD_INIT_DIR}"
  fi
}
