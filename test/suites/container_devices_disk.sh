test_container_devices_disk() {
  ensure_import_testimage

  lxc init testimage foo

  _container_devices_disk_type_arg
  _container_devices_disk_shift
  _container_devices_disk_mount
  _container_devices_disk_recursive
  _container_devices_raw_mount_options
  _container_devices_disk_ceph
  _container_devices_disk_cephfs
  _container_devices_disk_socket
  _container_devices_disk_char
  _container_devices_disk_patch

  lxc delete foo
}

_container_devices_disk_type_arg() {
  local err

  sub_test "Verify the disk device type cannot be given as a key=value pair"
  # Check that providing a key=value pair as the device type positional argument fails.
  err="$(! lxc config device add foo mnt-test "boot.priority=10" pool=default source=vol path=/mnt type=disk 2>&1 || echo fail)"
  [ "$(tail -1 <<< "${err}")" = 'Error: Invalid device type "boot.priority=10": the device type must be specified as the third positional argument' ]

  # Check that the device type cannot be specified as a key=value pair.
  err="$(! lxc config device add foo mnt-test disk source=/tmp path=/mnt type=disk 2>&1 || echo fail)"
  [ "$(tail -1 <<< "${err}")" = 'Error: The device type cannot be set as a key=value pair "type=disk", use the third positional argument instead' ]
}

_container_devices_idmapped_mounts_supported() {
  # `tmpfs` does not support idmapped mounts on kernels older than 6.3
  if [ "${LXD_TMPFS:-0}" = "1" ] && ! runsMinimumKernel 6.3; then
    echo "==> SKIP: tmpfs (LXD_TMPFS=${LXD_TMPFS}) idmapped mount requires a kernel >= 6.3"
    return 1
  fi

  if [ -n "${LXD_IDMAPPED_MOUNTS_DISABLE:-}" ]; then
    return 1
  fi

  if [ "$(storage_backend "$LXD_DIR")" = "zfs" ]; then
    # ZFS 2.2 is required for idmapped mounts support.
    zfs_version=$(zfs --version | grep -m 1 '^zfs-' | cut -d '-' -f 2)
    if [ "$(printf '%s\n' "$zfs_version" "2.2" | sort -V | head -n1)" = "$zfs_version" ]; then
      if [ "$zfs_version" != "2.2" ]; then
        echo "ZFS version is less than 2.2. Skipping idmapped mounts tests."
        return 1
      else
        echo "ZFS version is 2.2. Idmapped mounts are supported with ZFS."
      fi
    else
      echo "ZFS version is greater than 2.2. Idmapped mounts are supported with ZFS."
    fi
  fi

  return 0
}

_container_devices_disk_shift() {
  _container_devices_idmapped_mounts_supported || return

  sub_test "Hot-plug an unshifted then a shift=true device and verify ownership"
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

  sub_test "security.shifted custom volumes: verify shared ownership across privileged and isolated instances"
  # Test shifted custom volumes
  local POOL
  POOL="lxdtest-$(basename "${LXD_DIR}")"

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

  # Cannot modify security.shifted when the instance is running.
  ! lxc storage volume set "${POOL}" foo-shift security.shifted=false || false
  ! lxc storage volume unset "${POOL}" foo-shift security.shifted || false

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

  sub_test "Mount over an existing directory and verify permissions/ownership survive device removal"
  lxc exec foo -- mkdir -p /opt/target
  lxc exec foo -- chmod 754 /opt/target
  lxc exec foo -- chown 12345:12345 /opt/target
  lxc config device add foo bar disk source="$(mktemp -d -p "${TEST_DIR}" XXX)" path=/opt/target
  lxc config device remove foo bar

  echo "Check permissions and ownership remain after removal."
  [ "$(lxc exec foo -- stat -c '%a %u %g' /opt/target)" = "754 12345 12345" ]

  sub_test "Mount over an existing file and verify permissions/ownership/content survive device removal"
  echo "hello" | lxc file push - foo/opt/target-file
  lxc exec foo -- chmod 754 /opt/target-file
  lxc exec foo -- chown 12345:12345 /opt/target-file
  lxc config device add foo bar disk source="$(mktemp -p "${TEST_DIR}" XXX)" path=/opt/target-file
  lxc config device remove foo bar

  echo "Check permissions and ownership remain after removal."
  [ "$(lxc exec foo -- stat -c '%a %u %g' /opt/target-file)" = "754 12345 12345" ]

  echo "Check file content remains after removal."
  [ "$(lxc exec foo -- cat /opt/target-file)" = "hello" ]

  sub_test "Verify removal of a device that created its mount point under /dev"
  lxc config device add foo bar disk source=/dev/zero path=/dev/test
  lxc config device remove foo bar
  ! lxc exec foo -- mount | grep -F "/dev/test" || false
  ! lxc exec foo -- test -e /dev/test || false

  echo "Cleanup."

  lxc stop -f foo
}

_container_devices_disk_recursive() {
  lxc start foo

  sub_test "Hot-plug a recursive disk device and verify a submount nested in its source is visible too"
  # A source with a submount nested inside it (rather than a plain
  # directory), with recursive=true. The submount must be visible in the
  # instance too, not just the top-level mount.
  mkdir -p "${TEST_DIR}/recursive-source/nested"
  echo top-level-file > "${TEST_DIR}/recursive-source/top"
  mount -t tmpfs tmpfs "${TEST_DIR}/recursive-source/nested"
  echo nested-file > "${TEST_DIR}/recursive-source/nested/marker"

  lxc config device add foo recursive-mount disk source="${TEST_DIR}/recursive-source" path=/mnt/recursive recursive=true
  [ "$(lxc exec foo -- cat /mnt/recursive/top)" = "top-level-file" ]
  [ "$(lxc exec foo -- cat /mnt/recursive/nested/marker)" = "nested-file" ]
  lxc config device remove foo recursive-mount

  umount "${TEST_DIR}/recursive-source/nested"
  lxc stop foo -f

  # The top-level source below is a plain path under TEST_DIR, same as
  # _container_devices_disk_shift()'s basic-shifting phase, so it needs the
  # same idmapped-mount eligibility check (e.g. TEST_DIR itself can be tmpfs
  # via LXD_TMPFS=1, which only gained idmapped mount support in 6.3).
  _container_devices_idmapped_mounts_supported || return

  sub_test "Hot-plug a recursive AND shifted disk device and verify the nested submount is shifted too"
  # recursive=true and shift=true can be combined. A submount nested under
  # the top-level mount should be shifted too. Use a loopback ext4 mount
  # (rather than tmpfs) for the nested submount specifically, so that part
  # doesn't additionally depend on whatever filesystem TEST_DIR happens to
  # be on: ext4 has supported idmapped mounts since they were introduced
  # (5.12), unlike tmpfs which only gained support in 6.3.
  configure_loop_device recursive_shift_loop_file recursive_shift_loop_device
  # shellcheck disable=SC2154
  mkfs.ext4 -q "${recursive_shift_loop_device}"

  mkdir -p "${TEST_DIR}/recursive-shift-source/nested"
  touch "${TEST_DIR}/recursive-shift-source/top"
  chown 123:456 "${TEST_DIR}/recursive-shift-source/top"
  mount "${recursive_shift_loop_device}" "${TEST_DIR}/recursive-shift-source/nested"
  touch "${TEST_DIR}/recursive-shift-source/nested/marker"
  chown 123:456 "${TEST_DIR}/recursive-shift-source/nested/marker"

  lxc start foo
  lxc config device add foo recursive-shift-mount disk source="${TEST_DIR}/recursive-shift-source" path=/mnt/recursive-shift recursive=true shift=true
  [ "$(lxc exec foo -- stat -c '%u:%g' /mnt/recursive-shift/top)" = "123:456" ]
  [ "$(lxc exec foo -- stat -c '%u:%g' /mnt/recursive-shift/nested/marker)" = "123:456" ]
  lxc config device remove foo recursive-shift-mount
  lxc stop foo -f

  umount "${TEST_DIR}/recursive-shift-source/nested"
  # shellcheck disable=SC2154
  deconfigure_loop_device "${recursive_shift_loop_file}" "${recursive_shift_loop_device}"
}

_container_devices_raw_mount_options() {
  configure_loop_device loop_file_1 loop_device_1
  # shellcheck disable=SC2154
  mkfs.vfat "${loop_device_1}"

  lxc launch testimage foo-priv -c security.privileged=true

  sub_test "Hot-plug a loop device without raw.mount.options and verify default ownership/writability"
  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "0:0" ]
  lxc exec foo-priv -- touch /mnt/foo
  lxc config device remove foo-priv loop_raw_mount_options

  sub_test "Hot-plug a loop device with raw.mount.options and verify uid/gid/ro are applied"
  lxc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  [ "$(lxc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ]
  ! lxc exec foo-priv -- touch /mnt/foo || false
  lxc config device remove foo-priv loop_raw_mount_options

  sub_test "Cold-plug a loop device with raw.mount.options and verify uid/gid/ro are applied"
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
  if [ "$(storage_backend "$LXD_DIR")" != "ceph" ]; then
    return
  fi

  sub_test "Hot-plug a ceph RBD volume and verify it survives a restart"
  RBD_POOL_NAME=lxdtest-$(basename "${LXD_DIR}")-disk
  ceph osd pool create "${RBD_POOL_NAME}" 1
  rbd create --pool "${RBD_POOL_NAME}" --size 24M my-volume
  RBD_DEVICE=$(rbd map --pool "${RBD_POOL_NAME}" my-volume)
  mkfs.ext4 -E assume_storage_prezeroed=1 -m0 "${RBD_DEVICE}"
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
  if [ "$(storage_backend "$LXD_DIR")" != "ceph" ] || [ -z "${LXD_CEPH_CEPHFS:-}" ]; then
    return
  fi

  sub_test "Hot-plug a cephfs volume and verify it survives a restart"
  lxc launch testimage ceph-fs -c security.privileged=true
  lxc config device add ceph-fs fs disk source=cephfs:"${LXD_CEPH_CEPHFS}"/ ceph.user_name=admin ceph.cluster_name=ceph path=/cephfs
  lxc exec ceph-fs -- stat /cephfs
  lxc restart ceph-fs --force
  lxc exec ceph-fs -- stat /cephfs
  lxc delete -f ceph-fs
}

_container_devices_disk_socket() {
  lxc start foo

  sub_test "Hot-plug a unix socket disk device and verify it survives a restart"
  lxc config device add foo unix-socket disk source="${LXD_DIR}/unix.socket" path=/root/lxd.sock
  [ "$(lxc exec foo -- stat /root/lxd.sock -c '%F')" = "socket" ]
  lxc restart -f foo
  [ "$(lxc exec foo -- stat /root/lxd.sock -c '%F')" = "socket" ]
  lxc config device remove foo unix-socket

  lxc stop foo -f
}

_container_devices_disk_char() {
  lxc start foo

  sub_test "Hot-plug a character device disk and verify it survives a restart"
  lxc config device add foo char disk source=/dev/zero path=/root/zero
  [ "$(lxc exec foo -- stat /root/zero -c '%F')" = "character special file" ]
  lxc restart -f foo
  [ "$(lxc exec foo -- stat /root/zero -c '%F')" = "character special file" ]
  lxc config device remove foo char

  lxc stop foo -f
}

_container_devices_disk_patch() {
  sub_test "Add, update, and remove an instance device via PATCH"
  # Ensure no devices are present.
  [ "$(lxc config device list foo || echo fail)" = "" ]

  # Ensure a new device is added.
  lxc query -X PATCH /1.0/instances/foo -d '{"devices": {"tmp": {"type": "disk", "source": "/etc/os-release", "path": "/tmp/release"}}}'
  [ "$(lxc config device list foo)" = "tmp" ]

  # Ensure the device is updated.
  lxc query -X PATCH /1.0/instances/foo -d '{"devices": {"tmp": {"type": "disk", "source": "/etc/os-release", "path": "/tmp/release-new"}}}'
  [ "$(lxc config device get foo tmp path)" = "/tmp/release-new" ]

  # Ensure the device is not removed when patching with an empty devices object.
  lxc query -X PATCH /1.0/instances/foo -d '{"devices": {}}'
  [ "$(lxc config device list foo)" = "tmp" ]

  # Ensure the device is removed when patching with a null device.
  lxc query -X PATCH /1.0/instances/foo -d '{"devices": {"tmp": null }}'
  [ "$(lxc config device list foo || echo fail)" = "" ]
}
