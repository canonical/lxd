#!/bin/sh

lvm_setup() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Setting up lvm backend in ${LXD_DIR}"

  if ! which lvm >/dev/null 2>&1; then
    echo "Couldn't find the lvm binary"; false
  fi

  truncate -s 100G "${TEST_DIR}/$(basename "${LXD_DIR}").lvm"
  pvloopdev=$(losetup --show -f "${TEST_DIR}/$(basename "${LXD_DIR}").lvm")
  if [ ! -e "${pvloopdev}" ]; then
    echo "failed to setup loop"
    false
  fi
  echo "${pvloopdev}" > "${TEST_DIR}/$(basename "${LXD_DIR}").lvm.vg"

  pvcreate "${pvloopdev}"
  vgcreate "lxdtest-$(basename "${LXD_DIR}")" "${pvloopdev}"
}

lvm_configure() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Configuring lvm backend in ${LXD_DIR}"

  lxc config set storage.lvm_vg_name "lxdtest-$(basename "${LXD_DIR}")"
}

lvm_teardown() {
  local LXD_DIR
  LXD_DIR=$1

  echo "==> Tearing down lvm backend in ${LXD_DIR}"

  vgremove -f "lxdtest-$(basename "${LXD_DIR}")"
  pvremove -f "$(cat "${TEST_DIR}/$(basename "${LXD_DIR}").lvm.vg")"
  losetup -d "$(cat "${TEST_DIR}/$(basename "${LXD_DIR}").lvm.vg")"

  rm -f "${TEST_DIR}/$(basename "${LXD_DIR}").lvm"
  rm -f "${TEST_DIR}/$(basename "${LXD_DIR}").lvm.vg"
}
