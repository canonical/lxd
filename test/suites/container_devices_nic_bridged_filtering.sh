test_container_devices_nic_bridged_filtering() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  ctPrefix="nt$$"
  brName="lxdt$$"

  # Standard bridge with random subnet and a bunch of options.
  lxc network create "${brName}"
  lxc network set "${brName}" dns.mode managed
  lxc network set "${brName}" dns.domain blah
  lxc network set "${brName}" ipv4.nat true

  # Routing is required for container to container traffic as filtering requires br_netfilter module.
  # This then causes bridged traffic to go through the FORWARD chain in iptables.
  lxc network set "${brName}" ipv4.routing true
  lxc network set "${brName}" ipv6.routing true

  lxc network set "${brName}" ipv6.dhcp.stateful true
  lxc network set "${brName}" bridge.hwaddr 00:11:22:33:44:55
  lxc network set "${brName}" ipv4.address 192.0.2.1/24
  lxc network set "${brName}" ipv6.address 2001:db8::1/64
  [ "$(cat /sys/class/net/${brName}/address)" = "00:11:22:33:44:55" ]

  # Create profile for new containers.
  lxc profile copy default "${ctPrefix}"
  lxc profile device set "${ctPrefix}" eth0 parent "${brName}"
  lxc profile device set "${ctPrefix}" eth0 nictype "bridged"

  # Launch first container.
  lxc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}A" eth0 nic nictype=nic name=eth0 nictype=bridged parent="${brName}"
  lxc start "${ctPrefix}A"
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0

  # Launch second container.
  lxc init testimage "${ctPrefix}B" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}B" eth0 nic nictype=nic name=eth0 nictype=bridged parent="${brName}"
  lxc start "${ctPrefix}B"
  lxc exec "${ctPrefix}B" -- ip a add 192.0.2.3/24 dev eth0

  # Check basic connectivity without any filtering.
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.3

  # Enable MAC filtering on CT A and test.
  lxc config device set "${ctPrefix}A" eth0 security.mac_filtering true
  ctAMAC=$(lxc config get "${ctPrefix}A" volatile.eth0.hwaddr)

  # Check MAC filter is present in ebtables.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  if ! ebtables -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
      echo "MAC filter not applied in ebtables"
      false
  fi

  # Setup fake MAC inside container.
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address 00:11:22:33:44:56 up
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0

  # Check that ping is no longer working (i.e its filtered after fake MAC setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.1; then
      echo "MAC filter not working to host"
      false
  fi

  # Check that ping is no longer working (i.e its filtered after fake MAC setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.3; then
      echo "MAC filter not working to other container"
      false
  fi

  # Restore real MAC
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up

  # Check basic connectivity with MAC filtering but real MAC configured.
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.3

  # Stop CT A and check filters are cleaned up.
  lxc stop "${ctPrefix}A"
  if ebtables -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
      echo "MAC filter still applied in ebtables"
      false
  fi

  lxc delete -f "${ctPrefix}A"
  lxc delete -f "${ctPrefix}B"
  lxc network delete "${brName}"
  lxc profile delete "${ctPrefix}"
}
