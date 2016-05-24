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

  # CVE-2016-1582
  lxc launch testimage test-priv -c security.privileged=true

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-priv")
  if [ "${PERM}" != "700" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  lxc config set test-priv security.privileged false
  lxc restart test-priv --force
  lxc config set test-priv security.privileged true
  lxc restart test-priv --force

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-priv")
  if [ "${PERM}" != "700" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  lxc delete test-priv --force

  lxc launch testimage test-unpriv
  lxc config set test-unpriv security.privileged true
  lxc restart test-unpriv --force

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-unpriv")
  if [ "${PERM}" != "700" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  lxc delete test-unpriv --force
}
