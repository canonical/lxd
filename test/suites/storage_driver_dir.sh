#!/bin/bash

test_storage_driver_dir() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" != "dir" ]; then
    echo "==> SKIP: test_storage_driver_dir only supports 'dir', not ${lxd_backend}"
    return
  fi

  do_dir_on_empty_fs

  if uname -r | grep -- -kvm$; then
    echo "==> SKIP: the -kvm kernel flavor is does not support XFS quotas (CONFIG_XFS_QUOTA is not set)"
    return
  fi

  do_dir_xfs_project_quotas
}

do_dir_on_empty_fs() {
  # Create and mount a small ext4 filesystem.
  tmp_file="$(mktemp -p "${TEST_DIR}" disk.XXX)"
  fallocate -l 64MiB "${tmp_file}"
  mkfs.ext4 "${tmp_file}"

  mount_point="$(mktemp -d -p "${TEST_DIR}" mountpoint.XXX)"
  mount -o loop "${tmp_file}" "${mount_point}"

  if [ ! -d "${mount_point}/lost+found" ]; then
    echo "Error: Expected ${mount_point}/lost+found subdirectory to exist"
    return 1
  fi

  # Create storage pool in the root path of the mounted filesystem where lost+found subdirectory exists.
  lxc storage create s1 dir source="${mount_point}"
  lxc storage delete s1

  # Create storage pool in the non-root path of the mounted filesystem where lost+found subdirectory exists.
  mkdir -p "${mount_point}/dir/lost+found"
  if lxc storage create s1 dir source="${mount_point}/dir"; then
    echo "Error: Storage pool creation should have failed: Directory '${mount_point}/dir' is not empty"
    return 1
  fi

  # Cleanup.
  umount "${mount_point}"
  rm -rf "${mount_point}"
  rm -f "${tmp_file}"
}

do_dir_xfs_project_quotas() {
  echo "==> Create and mount a small XFS filesystem with project quotas."

  # XFS filesystem must be larger than 300MB.
  tmp_file="$(mktemp -p "${TEST_DIR}" disk.XXX)"
  fallocate -l 1G "${tmp_file}"
  mkfs.xfs "${tmp_file}"

  mount_point="$(mktemp -d -p "${TEST_DIR}" mountpoint.XXX)"
  mount -o loop -o prjquota "${tmp_file}" "${mount_point}"

  echo "==> Verify that the filesystem is mounted and project quotas are enabled."
  if ! mount | grep -E -w "${mount_point}.*prjquota" ; then
    echo "Error: prjquota is not enabled on ${mount_point}"
    return 1
  fi

  echo "==> XFS filesystem with project quotas is ready."

  echo "==> Create LXD dir storage pool backed by XFS."
  lxc storage create xfs_pool dir source="${mount_point}"

  echo "==> Create a profile that uses xfs_pool as root disk. Limit the root disk to 850MiB."
  lxc profile create xfs_profile
  lxc profile device add xfs_profile root disk pool=xfs_pool path=/ size=850MiB

  echo "==> Launch a container using the dir storage pool backed by XFS."
  lxc launch images:alpine/edge foo -p default -p xfs_profile

  container_path="${mount_point}/containers/foo"
  project_id=$(lsattr -p "${container_path}" | awk '{print $1}' | head -n 1)

  echo "==> Check that XFS project quota matches the container's root disk size limit."
  project_hard_quota=$(xfs_quota -x -c 'report -h' "${mount_point}" | awk -v id="${project_id}" '$1 ~ id {print $4}')
  if [ -z "${project_hard_quota}" ]; then
     echo "Error: XFS project size hard quota not found"
     return 1
  elif [ "${project_hard_quota}" != "850M" ]; then
     echo "Error: XFS project size hard quota not matching the container's root disk size limit"
     return 1
  fi

  echo "==> Delete the container."
  lxc delete foo --force

  echo "==> Remove the profile."
  lxc profile delete xfs_profile

  echo "==> Remove the storage pool."
  lxc storage delete xfs_pool

  echo "==> Cleanup the loopback file."
  umount "${mount_point}"
  rm -rf "${mount_point}"
  rm -f "${tmp_file}"
}
