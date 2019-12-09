test_container_devices_disk() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage foo

  test_container_devices_disk_shift
  test_container_devices_raw_mount_options
  test_container_devices_disk_ceph
  test_container_devices_disk_cephfs

  lxc delete -f foo
}

test_container_devices_disk_shift() {
  if ! grep -q shiftfs /proc/filesystems; then
    return
  fi

  # Test basic shiftfs
  mkdir -p "${TEST_DIR}/shift-source"
  touch "${TEST_DIR}/shift-source/a"
  chown 123:456 "${TEST_DIR}/shift-source/a"

  lxc config device add foo shiftfs disk source="${TEST_DIR}/shift-source" path=/mnt
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "65534:65534" ] || false
  lxc config device remove foo shiftfs

  lxc config device add foo shiftfs disk source="${TEST_DIR}/shift-source" path=/mnt shift=true
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false

  lxc stop foo -f
  lxc start foo
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  lxc config device remove foo shiftfs
  lxc stop foo -f

  # Test shifted custom volumes
  POOL=$(lxc profile device get default root pool)
  lxc storage volume create "${POOL}" foo-shift security.shifted=true

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

test_container_devices_raw_mount_options() {
  configure_loop_device loop_file_1 loop_device_1
  # shellcheck disable=SC2154
  mkfs.vfat "${loop_device_1}"

  lxc launch testimage foo-priv -c security.privileged=true

  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "0:0" ] || false
  lxc config device remove foo-priv loop_raw_mount_options

  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  lxc config device remove foo-priv loop_raw_mount_options

  lxc stop foo-priv -f
  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456
  lxc start foo-priv

  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  lxc config device remove foo-priv loop_raw_mount_options

  lxc delete -f foo-priv
  # shellcheck disable=SC2154
  deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
}

test_container_devices_disk_ceph() {
  # shellcheck disable=SC2039
  local LXD_BACKEND

  LXD_BACKEND=$(storage_backend "$LXD_DIR")
  if ! [ "${LXD_BACKEND}" = "ceph" ]; then
    return
  fi

  RBD_POOL_NAME=lxdtest-$(basename "${LXD_DIR}")-disk
  ceph osd pool create "${RBD_POOL_NAME}" 1
  rbd create --pool "${RBD_POOL_NAME}" --size 50MB my-volume
  RBD_DEVICE=$(rbd map --pool "${RBD_POOL_NAME}" my-volume)
  mkfs.ext4 -m0 "${RBD_DEVICE}"
  rbd unmap "${RBD_DEVICE}"

  lxc launch testimage ceph-disk -c security.privileged=true
  lxc config device add ceph-disk rbd disk source=ceph:"${RBD_POOL_NAME}"/my-volume ceph.user_name=admin ceph.cluster_name=ceph path=/ceph
  lxc exec ceph-disk -- stat /ceph/lost+found
  lxc restart ceph-disk --force
  lxc exec ceph-disk -- stat /ceph/lost+found
  lxc delete -f ceph-disk
}

test_container_devices_disk_cephfs() {
  # shellcheck disable=SC2039
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
