#!/bin/sh

lvm_setup() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Setting up lvm backend in ${LXD_DIR}"

  if ! which lvm >/dev/null 2>&1; then
    echo "Couldn't find the lvm binary"; false
  fi
}

lvm_configure() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Configuring lvm backend in ${LXD_DIR}"

  lxc storage create "lxdtest-$(basename "${LXD_DIR}")" lvm volume.size=25MB
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"
}

lvm_teardown() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Tearing down lvm backend in ${LXD_DIR}"
}
