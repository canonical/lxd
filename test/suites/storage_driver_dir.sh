#!/bin/bash

test_storage_driver_dir() {
  do_dir_on_empty_fs
}

do_dir_on_empty_fs() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" != "dir" ]; then
    return
  fi

  # Create and mount 32MiB ext4 filesystem.
  tmp_file=$(mktemp "${TEST_DIR}/disk.XXXXXX")
  dd if=/dev/zero of="${tmp_file}" bs=1M count=64
  mkfs.ext4 "${tmp_file}"

  mount_point=$(mktemp -d "${TEST_DIR}/mountpoint.XXXXXX")
  sudo mount -o loop "${tmp_file}" "${mount_point}"

  if [ ! -d "${mount_point}/lost+found" ]; then
    echo "Error: Expected ${mount_point}/lost+found subdirectory to exist"
    return 1
  fi

  # Create storage pool in the root path of the mounted filesystem where lost+found subdirectory exists.
  lxc storage create s1 dir source="${mount_point}"
  lxc storage delete s1

  # Create storage pool in the non-root path of the mounted filesystem where lost+found subdirectory exists.
  sudo mkdir -p "${mount_point}/dir/lost+found"
  if lxc storage create s1 dir source="${mount_point}/dir"; then
    echo "Error: Storage pool creation should have failed: Directory '${mount_point}/dir' is not empty"
    return 1
  fi

  # Cleanup.
  sudo umount "${mount_point}"
  rm -rf "${mount_point}"
  rm -f "${tmp_file}"
}
