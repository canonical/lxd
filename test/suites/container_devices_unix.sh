test_container_devices_unix() {
  lxdFSMonitorDriver=${LXD_FSMONITOR_DRIVER:-}

  shutdown_lxd "${LXD_DIR}"
  export LXD_FSMONITOR_DRIVER="fanotify"
  respawn_lxd "${LXD_DIR}" true
  _container_devices_unix "unix-block"
  _container_devices_unix "unix-char"

  shutdown_lxd "${LXD_DIR}"
  export LXD_FSMONITOR_DRIVER="inotify"
  respawn_lxd "${LXD_DIR}" true
  _container_devices_unix "unix-block"
  _container_devices_unix "unix-char"

  shutdown_lxd "${LXD_DIR}"
  export LXD_FSMONITOR_DRIVER="${lxdFSMonitorDriver}"
  respawn_lxd "${LXD_DIR}" true
}

_container_devices_unix() {
  deviceType=$1
  deviceTypeCode=""
  deviceTypeDesc=""

  if [ "$deviceType" = "unix-block" ]; then
    deviceTypeCode="b"
    deviceTypeDesc="block special file"
  fi

  if [ "$deviceType" = "unix-char" ]; then
    deviceTypeCode="c"
    deviceTypeDesc="character special file"
  fi

  if [ "$deviceTypeCode" = "" ] || [ "$deviceTypeDesc" = "" ]; then
    echo "invalid device type/desc specified in test"
    false
  fi

  ensure_import_testimage
  ctName="ct$$"
  lxc launch testimage "${ctName}"

  # Create a test unix device.
  testDev="${TEST_DIR}"/dev/testdev-"${ctName}"
  mknod "${testDev}" "${deviceTypeCode}" 0 0

  # Check adding a device without source or path fails.
  ! lxc config device add "${ctName}" test-dev-invalid "${deviceType}" || false
  ! lxc config device add "${ctName}" test-dev-invalid "${deviceType}" required=false || false

  # Check adding a device with missing source and no major/minor numbers fails.
  ! lxc config device add "${ctName}" test-dev-invalid "${deviceType}" path=/tmp/testdevmissing || false

  # Check adding a required (default) missing device fails.
  ! lxc config device add "${ctName}" test-dev-invalid "${deviceType}" path=/tmp/testdevmissing || false
  ! lxc config device add "${ctName}" test-dev-invalid "${deviceType}" path=/tmp/testdevmissing required=true || false

  # Add device based on existing device, check its host-side name, default mode, major/minor inherited, and mounted in container.
  lxc config device add "${ctName}" test-dev1 "${deviceType}" source="${testDev}" path=/tmp/testdev
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev)" = "${deviceTypeDesc} 660 0 0" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev1.tmp-testdev)" = "${deviceTypeDesc} 660 0 0" ]

  # Add device with same dest path as existing device, but with different mode and major/minor and check original isn't replaced inside instance.
  lxc config device add "${ctName}" test-dev2 "${deviceType}" source="${testDev}" path=/tmp/testdev major=1 minor=1 mode=600
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev)" = "${deviceTypeDesc} 660 0 0" ]

  # Check a new host side file was created with correct attributes.
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev2.tmp-testdev)" = "${deviceTypeDesc} 600 1 1" ]

  # Remove dupe device and check the original is still mounted.
  lxc config device remove "${ctName}" test-dev2
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev)" = "${deviceTypeDesc} 660 0 0" ]

  # Check dupe device host side file is removed though.
  if [ -e "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev2.tmp-testdev ]; then
    echo "test-dev2 host side file not removed"
    false
  fi

  # Add new device with custom mode and check it creates correctly on boot.
  lxc stop -f "${ctName}"
  lxc config device add "${ctName}" test-dev3 "${deviceType}" source="${testDev}" path=/tmp/testdev3 major=1 minor=1 mode=600
  lxc start "${ctName}"
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev3"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev3)" = "${deviceTypeDesc} 600 1 1" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev3.tmp-testdev3)" = "${deviceTypeDesc} 600 1 1" ]
  lxc config device remove "${ctName}" test-dev3

  # Add new device without a source, but with a path and major and minor numbers.
  lxc config device add "${ctName}" test-dev4 "${deviceType}" path=/tmp/testdev4 major=0 minor=2 mode=777
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev4"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev4)" = "${deviceTypeDesc} 777 0 2" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev4.tmp-testdev4)" = "${deviceTypeDesc} 777 0 2" ]
  lxc config device remove "${ctName}" test-dev4

  lxc stop -f "${ctName}"
  lxc config device remove "${ctName}" test-dev1
  rm "${testDev}"

  # Add a device that is missing, but not required, start instance and then add it.
  lxc config device add "${ctName}" test-dev-dynamic "${deviceType}" required=false source="${testDev}" path=/tmp/testdev
  lxc start "${ctName}"
  ! test -e "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev || false
  mknod "${testDev}" "${deviceTypeCode}" 0 0
  sleep 2
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev)" = "${deviceTypeDesc} 660 0 0" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev)" = "${deviceTypeDesc} 660 0 0" ]

  # Check removal of mount point devices created in /dev.
  lxc config device add "${ctName}" test-dev5 disk source=/dev/zero path=/dev/test
  lxc config device remove "${ctName}" test-dev5
  ! lxc exec "${ctName}" -- mount | grep -F "/dev/test" || false
  ! lxc exec "${ctName}" -- test -e /dev/test || false

  # Remove host side device and check it is dynamically removed from instance.
  rm "${testDev}"
  sleep 1
  ! lxc exec "${ctName}" -- mount | grep -F "/tmp/testdev" || false
  lxc exec "${ctName}" -- test -e /tmp/testdev
  ! test -e "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev || false

  # Leave instance running, restart LXD, then add device back to check LXD start time inotify works.
  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true
  mknod "${testDev}" "${deviceTypeCode}" 0 0
  sleep 2
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev)" = "${deviceTypeDesc} 660 0 0" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev)" = "${deviceTypeDesc} 660 0 0" ]

  # Update device's source, check old instance device is removed and new watchers set up.
  rm "${testDev}"
  testDevSubDir="${testDev}"/subdev
  ls -la "${TEST_DIR}"
  lxc config device set "${ctName}" test-dev-dynamic source="${testDevSubDir}"
  ! lxc exec "${ctName}" -- mount | grep -F "/tmp/testdev" || false
  lxc exec "${ctName}" -- test -e /tmp/testdev
  ! test -e "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev || false

  mkdir "${testDev}"
  mknod "${testDevSubDir}" "${deviceTypeCode}" 0 0
  sleep 2
  lxc exec "${ctName}" -- mount | grep "/tmp/testdev"
  [ "$(lxc exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev)" = "${deviceTypeDesc} 660 0 0" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev)" = "${deviceTypeDesc} 660 0 0" ]

  # Cleanup.
  rm -rvf "${testDev}"

  sleep 1
  ! lxc exec "${ctName}" -- mount | grep -F "/tmp/testdev" || false
  lxc exec "${ctName}" -- test -e /tmp/testdev
  ! test -e "${LXD_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev || false

  lxc delete -f "${ctName}"

  # Check multiple instances sharing same watcher.
  lxc launch testimage "${ctName}1"
  lxc config device add "${ctName}1" test-dev-dynamic "${deviceType}" required=false source="${testDev}" path=/tmp/testdev1
  lxc launch testimage "${ctName}2"
  lxc config device add "${ctName}2" test-dev-dynamic "${deviceType}" required=false source="${testDev}" path=/tmp/testdev2
  mknod "${testDev}" "${deviceTypeCode}" 0 0
  sleep 2
  lxc exec "${ctName}1" -- mount | grep "/tmp/testdev1"
  [ "$(lxc exec "${ctName}1" -- stat -c '%F %a %t %T' /tmp/testdev1)" = "${deviceTypeDesc} 660 0 0" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"1/unix.test--dev--dynamic.tmp-testdev1)" = "${deviceTypeDesc} 660 0 0" ]
  lxc exec "${ctName}2" -- mount | grep "/tmp/testdev2"
  [ "$(lxc exec "${ctName}2" -- stat -c '%F %a %t %T' /tmp/testdev2)" = "${deviceTypeDesc} 660 0 0" ]
  [ "$(stat -c '%F %a %t %T' "${LXD_DIR}"/devices/"${ctName}"2/unix.test--dev--dynamic.tmp-testdev2)" = "${deviceTypeDesc} 660 0 0" ]

  # Stop one instance, then remove the host device to check the watcher still works after first
  # instance was stopped. This checks the removal logic when multiple containers share watch path.
  lxc stop -f "${ctName}1"
  rm "${testDev}"
  sleep 1
  ! lxc exec "${ctName}2" -- mount | grep -F "/tmp/testdev2" || false
  lxc exec "${ctName}2" -- test -e /tmp/testdev2
  ! test -e "${LXD_DIR}"/devices/"${ctName}"2/unix.test--dev--dynamic.tmp-testdev2 || false
  lxc delete -f "${ctName}1"
  lxc delete -f "${ctName}2"
}

