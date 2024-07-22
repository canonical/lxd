test_container_devices_none() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"
  ctName="ct$$"
  lxc launch testimage "${ctName}"

  # Check eth0 interface exists.
  lxc exec "${ctName}" -- stat /sys/class/net/eth0

  # Add none device to remove eth0 interface (simulating network disruption).
  lxc config device add "${ctName}" eth0 none
  ! lxc exec "${ctName}" -- stat /sys/class/net/eth0 || false

  # Remove device and check eth0 interface is added back.
  lxc config device rm "${ctName}" eth0
  lxc exec "${ctName}" -- stat /sys/class/net/eth0

  # Clean up
  lxc rm -f "${ctName}"
}
