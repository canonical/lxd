test_container_devices_disk() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init testimage foo

  _container_devices_disk_shift
  _container_devices_disk_mount
  _container_devices_raw_mount_options
  _container_devices_disk_ceph
  _container_devices_disk_cephfs
  _container_devices_disk_socket
  _container_devices_disk_char
  _container_devices_disk_patch

  lxc delete foo
}

_container_devices_disk_shift() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  # `tmpfs` does not support idmapped mounts on kernels older than 6.3
  if [ "${LXD_TMPFS:-0}" = "1" ] && ! runsMinimumKernel 6.3; then
    echo "==> SKIP: tmpfs (LXD_TMPFS=${LXD_TMPFS}) idmapped mount requires a kernel >= 6.3"
    return
  fi

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
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "65534:65534" ]
  lxc config device remove foo idmapped_mount

  lxc config device add foo idmapped_mount disk source="${TEST_DIR}/shift-source" path=/mnt shift=true
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ]

  lxc restart foo -f
  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ]
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

  [ "$(lxc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ]
  [ "$(lxc exec foo-priv -- stat /mnt/a -c '%u:%g')" = "123:456" ]
  [ "$(lxc exec foo-isol1 -- stat /mnt/a -c '%u:%g')" = "123:456" ]
  [ "$(lxc exec foo-isol2 -- stat /mnt/a -c '%u:%g')" = "123:456" ]

  lxc delete -f foo-priv foo-isol1 foo-isol2
  lxc config device remove foo shifted
  lxc storage volume delete "${POOL}" foo-shift
  lxc stop foo -f
}

_container_devices_disk_mount() {
  lxc start foo
  # Add a mount that points to an existing path in the instance.
  lxc exec foo -- mkdir -p /opt/target
  lxc exec foo -- chmod 754 /opt/target
  lxc exec foo -- chown 12345:12345 /opt/target
  lxc config device add foo bar disk source="$(mktemp -d -p "${TEST_DIR}" XXX)" path=/opt/target
  lxc config device remove foo bar
  # Check permissions and ownership remain after removal.
  [ "$(lxc exec foo -- stat -c '%a %u %g' /opt/target)" = "754 12345 12345" ]

  # Add a mount point that points to an existing file in the instance.
  lxc exec foo -- sh -c 'echo "hello" > /opt/target-file'
  echo "hello" | lxc file push - foo/opt/target-file
  lxc exec foo -- chmod 754 /opt/target-file
  lxc exec foo -- chown 12345:12345 /opt/target-file
  lxc config device add foo bar disk source="$(mktemp -p "${TEST_DIR}" XXX)" path=/opt/target-file
  lxc config device remove foo bar
  # Check permissions and ownership remain after removal.
  [ "$(lxc exec foo -- stat -c '%a %u %g' /opt/target-file)" = "754 12345 12345" ]
  # Check file content remains after removal.
  [ "$(lxc exec foo -- cat /opt/target-file)" = "hello" ]

  # Check removal of mount point devices created in /dev.
  lxc config device add foo bar disk source=/dev/zero path=/dev/test
  lxc config device remove foo bar
  ! lxc exec foo -- mount | grep -F "/dev/test" || false
  ! lxc exec foo -- test -e /dev/test || false

  lxc stop -f foo
}

_container_devices_raw_mount_options() {
  configure_loop_device loop_file_1 loop_device_1
  # shellcheck disable=SC2154
  mkfs.vfat "${loop_device_1}"

  lxc launch testimage foo-priv -c security.privileged=true

  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "0:0" ]
  lxc exec foo-priv -- touch /mnt/foo
  lxc config device remove foo-priv loop_raw_mount_options

  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ]
  ! lxc exec foo-priv -- touch /mnt/foo || false
  lxc config device remove foo-priv loop_raw_mount_options

  lxc stop foo-priv -f
  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  lxc start foo-priv
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ]
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
  [ "$(lxc exec foo -- stat /root/lxd.sock -c '%F')" = "socket" ]
  lxc restart -f foo
  [ "$(lxc exec foo -- stat /root/lxd.sock -c '%F')" = "socket" ]
  lxc config device remove foo unix-socket
  lxc stop foo -f
}

_container_devices_disk_char() {
  lxc start foo
  lxc config device add foo char disk source=/dev/zero path=/root/zero
  [ "$(lxc exec foo -- stat /root/zero -c '%F')" = "character special file" ]
  lxc restart -f foo
  [ "$(lxc exec foo -- stat /root/zero -c '%F')" = "character special file" ]
  lxc config device remove foo char
  lxc stop foo -f
}

_container_devices_disk_patch() {
  lxc init c1 --empty

  # Ensure no devices are present.
  [ "$(lxc config device list c1 | awk 'NF' | wc -l)" -eq 0 ]

  # Ensure a new device is added.
  lxc query -X PATCH /1.0/instances/c1 -d '{\"devices\": {\"tmp\": {\"type\": \"disk\", \"source\": \"/etc/os-release\", \"path\": \"/tmp/release\"}}}'
  [ "$(lxc config device list c1 | awk 'NF' | wc -l)" -eq 1 ]

  # Ensure the device is updated.
  lxc query -X PATCH /1.0/instances/c1 -d '{\"devices\": {\"tmp\": {\"type\": \"disk\", \"source\": \"/etc/os-release\", \"path\": \"/tmp/release-new\"}}}'
  [ "$(lxc config device get c1 tmp path)" = "/tmp/release-new" ]

  # Ensure the device is not removed when patching with an empty devices object.
  lxc query -X PATCH /1.0/instances/c1 -d '{\"devices\": {}}'
  [ "$(lxc config device list c1 | awk 'NF' | wc -l)" -eq 1 ]

  # Ensure the device is removed when patching with a null device.
  lxc query -X PATCH /1.0/instances/c1 -d '{\"devices\": {\"tmp\": null }}'
  [ "$(lxc config device list c1 | awk 'NF' | wc -l)" -eq 0 ]

  lxc delete --force c1
}
