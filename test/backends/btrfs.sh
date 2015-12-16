#!/bin/sh

btrfs_setup() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Setting up btrfs backend in ${LXD_DIR}"

  if ! which btrfs >/dev/null 2>&1; then
    echo "Couldn't find the btrfs binary"; false
  fi

  truncate -s 100G "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs"
  mkfs.btrfs "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs"

  mount -o loop "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs" "${LXD_DIR}"
}

btrfs_configure() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Configuring btrfs backend in ${LXD_DIR}"
}

btrfs_teardown() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Tearing down btrfs backend in ${LXD_DIR}"

  umount -l "${LXD_DIR}"
  rm -f "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs"
}
