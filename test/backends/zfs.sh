#!/bin/sh

zfs_setup() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Setting up ZFS backend in ${LXD_DIR}"

  if ! which zfs >/dev/null 2>&1; then
    echo "Couldn't find zfs binary"; false
  fi

  truncate -s 100G "${LXD_DIR}/zfspool"
  # prefix lxdtest- here, as zfs pools must start with a letter, but tempdir
  # won't necessarily generate one that does.
  zpool create "lxdtest-$(basename "${LXD_DIR}")" "${LXD_DIR}/zfspool" -m none
}

zfs_configure() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Configuring ZFS backend in ${LXD_DIR}"

  lxc config set storage.zfs_pool_name "lxdtest-$(basename "${LXD_DIR}")"
}

zfs_teardown() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Tearing down ZFS backend in ${LXD_DIR}"

  # Wait up to 5s for zpool destroy to succeed
  SUCCESS=0

  # shellcheck disable=SC2034
  for i in $(seq 10); do
    zpool destroy -f "lxdtest-$(basename "${LXD_DIR}")" >/dev/null 2>&1 || true
    if ! zpool list -o name -H | grep -q "^lxdtest-$(basename "${LXD_DIR}")"; then
      SUCCESS=1
      break
    fi

    sleep 0.5
  done

  if [ "${SUCCESS}" = "0" ]; then
    echo "Failed to destroy the zpool"
    false
  fi
}
