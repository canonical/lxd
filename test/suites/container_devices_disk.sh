test_container_devices_disk() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init testimage foo

  _container_devices_disk_shift
  _container_devices_raw_mount_options
  _container_devices_disk_ceph
  _container_devices_disk_cephfs
  _container_devices_disk_socket
  _container_devices_disk_char

  lxc delete -f foo
}

_container_devices_disk_shift() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  if [ -n "${LXD_IDMAPPED_MOUNTS_DISABLE:-}" ]; then
    return
  fi

  if [ "${lxd_backend}" = "zfs" ]; then
    # ZFS 2.2 is required for idmapped mounts support.
    zfs_version=$(zfs --version | grep -m 1 '^zfs-' | cut -d '-' -f 2)
    if [ "$(printf '%s\n' "$zfs_version" "2.2" | sort -V | head -n1)" = "$zfs_version" ]; then
      if [ "$zfs_version" != "2.2" ]; then
        echo "ZFS version is less than 2.2. Skipping idmapped mounts tests."
        return
      else
        echo "ZFS version is 2.2. Idmapped mounts are supported with ZFS."
      fi
    else
      echo "ZFS version is greater than 2.2. Idmapped mounts are supported with ZFS."
    fi
  fi

  # Test basic shifting
  mkdir -p "${TEST_DIR}/shift-source"
  touch "${TEST_DIR}/shift-source/a"
  chown 123:456 "${TEST_DIR}/shift-source/a"

  lxc start foo
  lxc config device add foo idmapped_mount disk source="${TEST_DIR}/shift-source" path=/mnt
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "65534:65534" ] || false
  lxc config device remove foo idmapped_mount

  lxc config device add foo idmapped_mount disk source="${TEST_DIR}/shift-source" path=/mnt shift=true
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false

  lxc stop foo -f
  lxc start foo
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  lxc config device remove foo idmapped_mount
  lxc stop foo -f

  # Test shifted custom volumes
  POOL=$(lxc profile device get default root pool)

  # Cannot set both security.shifted and security.unmapped.
  ! lxc storage volume create "${POOL}" foo-shift security.shifted=true security.unmapped=true || false

  lxc storage volume create "${POOL}" foo-shift security.shifted=true

  # Cannot set both security.shifted and security.unmapped.
  ! lxc storage volume set "${POOL}" foo-shift security.unmapped=true || false

  lxc start foo
  lxc launch testimage foo-priv -c security.privileged=true
  lxc launch testimage foo-isol1 -c security.idmap.isolated=true
  lxc launch testimage foo-isol2 -c security.idmap.isolated=true

  lxc config device add foo shifted disk pool="${POOL}" source=foo-shift path=/mnt
  lxc config device add foo-priv shifted disk pool="${POOL}" source=foo-shift path=/mnt
  lxc config device add foo-isol1 shifted disk pool="${POOL}" source=foo-shift path=/mnt
  lxc config device add foo-isol2 shifted disk pool="${POOL}" source=foo-shift path=/mnt

  lxc exec foo -- touch /mnt/a
  lxc exec foo -- chown 123:456 /mnt/a

  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(lxc exec foo-priv -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(lxc exec foo-isol1 -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(lxc exec foo-isol2 -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false

  lxc delete -f foo-priv foo-isol1 foo-isol2
  lxc config device remove foo shifted
  lxc storage volume delete "${POOL}" foo-shift
  lxc stop foo -f
}

_container_devices_raw_mount_options() {
  configure_loop_device loop_file_1 loop_device_1
  # shellcheck disable=SC2154
  mkfs.vfat "${loop_device_1}"

  lxc launch testimage foo-priv -c security.privileged=true

  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "0:0" ] || false
  lxc exec foo-priv -- touch /mnt/foo
  lxc config device remove foo-priv loop_raw_mount_options

  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  ! lxc exec foo-priv -- touch /mnt/foo || false
  lxc config device remove foo-priv loop_raw_mount_options

  lxc stop foo-priv -f
  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  lxc start foo-priv
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  ! lxc exec foo-priv -- touch /mnt/foo || false
  lxc config device remove foo-priv loop_raw_mount_options

  lxc delete -f foo-priv
  # shellcheck disable=SC2154
  deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
}

_container_devices_disk_ceph() {
  local LXD_BACKEND

  LXD_BACKEND=$(storage_backend "$LXD_DIR")
  if ! [ "${LXD_BACKEND}" = "ceph" ]; then
    return
  fi

  RBD_POOL_NAME=lxdtest-$(basename "${LXD_DIR}")-disk
  ceph osd pool create "${RBD_POOL_NAME}" 1
  rbd create --pool "${RBD_POOL_NAME}" --size 50M my-volume
  RBD_DEVICE=$(rbd map --pool "${RBD_POOL_NAME}" my-volume)
  mkfs.ext4 -m0 "${RBD_DEVICE}"
  rbd unmap "${RBD_DEVICE}"

  lxc launch testimage ceph-disk -c security.privileged=true
  lxc config device add ceph-disk rbd disk source=ceph:"${RBD_POOL_NAME}"/my-volume ceph.user_name=admin ceph.cluster_name=ceph path=/ceph
  lxc exec ceph-disk -- stat /ceph/lost+found
  lxc restart ceph-disk --force
  lxc exec ceph-disk -- stat /ceph/lost+found
  lxc delete -f ceph-disk
  ceph osd pool rm "${RBD_POOL_NAME}" "${RBD_POOL_NAME}" --yes-i-really-really-mean-it
}

_container_devices_disk_cephfs() {
  local LXD_BACKEND

  LXD_BACKEND=$(storage_backend "$LXD_DIR")
  if [ "${LXD_BACKEND}" != "ceph" ] || [ -z "${LXD_CEPH_CEPHFS:-}" ]; then
    return
  fi

  lxc launch testimage ceph-fs -c security.privileged=true
  lxc config device add ceph-fs fs disk source=cephfs:"${LXD_CEPH_CEPHFS}"/ ceph.user_name=admin ceph.cluster_name=ceph path=/cephfs
  lxc exec ceph-fs -- stat /cephfs
  lxc restart ceph-fs --force
  lxc exec ceph-fs -- stat /cephfs
  lxc delete -f ceph-fs
}

_container_devices_disk_socket() {
  lxc start foo
  lxc config device add foo unix-socket disk source="${LXD_DIR}/unix.socket" path=/root/lxd.sock
  [ "$(lxc exec foo -- stat /root/lxd.sock -c '%F')" = "socket" ] || false
  lxc restart -f foo
  [ "$(lxc exec foo -- stat /root/lxd.sock -c '%F')" = "socket" ] || false
  lxc config device remove foo unix-socket
  lxc stop foo -f
}

_container_devices_disk_char() {
  lxc start foo
  lxc config device add foo char disk source=/dev/zero path=/root/zero
  [ "$(lxc exec foo -- stat /root/zero -c '%F')" = "character special file" ] || false
  lxc restart -f foo
  [ "$(lxc exec foo -- stat /root/zero -c '%F')" = "character special file" ] || false
  lxc config device remove foo char
  lxc stop foo -f
}
