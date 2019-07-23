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

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Create profile for new containers.
  lxc profile copy default "${ctPrefix}"

  # Modifiy profile nictype and parent in atomic operation to ensure validation passes.
  lxc profile show "${ctPrefix}" | sed  "s/nictype: p2p/nictype: bridged\n    parent: ${brName}/" | lxc profile edit "${ctPrefix}"

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
  lxc stop -f "${ctPrefix}A"
  if ebtables -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
      echo "MAC filter still applied in ebtables"
      false
  fi

  # Add a fake IPv4 and check connectivity
  lxc start "${ctPrefix}A"
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.254/24 dev eth0
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.3

  # Enable IPv4 filtering on CT A and test (disable security.mac_filtering to check its applied too).
  lxc config device set "${ctPrefix}A" eth0 ipv4.address 192.0.2.2
  lxc config device set "${ctPrefix}A" eth0 security.mac_filtering false
  lxc config device set "${ctPrefix}A" eth0 security.ipv4_filtering true

  # Check MAC filter is present in ebtables.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  if ! ebtables -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
      echo "mac filter not applied as part of ipv4_filtering in ebtables"
      false
  fi

  # Check IPv4 filter is present in ebtables.
  if ! ebtables -L --Lmac2 --Lx | grep -e "192.0.2.2" ; then
      echo "IPv4 filter not applied as part of ipv4_filtering in ebtables"
      false
  fi

  # Check DHCPv4 allocation still works.
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up
  lxc exec "${ctPrefix}A" -- udhcpc -i eth0 -n
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0

  # Check basic connectivity with IPv4 filtering and real IPs configured.
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.3

  # Add a fake IP
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.254/24 dev eth0

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.1; then
      echo "IPv4 filter not working to host"
      false
  fi

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W1 192.0.2.3; then
      echo "IPv4 filter not working to other container"
      false
  fi

  # Stop CT A and check filters are cleaned up.
  lxc stop -f "${ctPrefix}A"
  if ebtables -L --Lmac2 --Lx | grep -e "${ctAHost}" ; then
      echo "IP filter still applied as part of ipv4_filtering in ebtables"
      false
  fi

  # Remove static IP and check IP filter works with previous DHCP lease.
  rm "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A"
  lxc config device unset "${ctPrefix}A" eth0 ipv4.address
  lxc start "${ctPrefix}A"
  if ! grep "192.0.2.2" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A" ; then
    echo "dnsmasq host config doesnt contain previous lease as static IPv4 config"
    false
  fi

  lxc stop -f "${ctPrefix}A"
  lxc config device set "${ctPrefix}A" eth0 security.ipv4_filtering false
  rm "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A"

  # Simulate 192.0.2.2 being used by another container, next free IP is 192.0.2.3
  kill "$(cat "${LXD_DIR}"/networks/"${brName}"/dnsmasq.pid)"
  echo "$(date --date="1hour" +%s) 00:16:3e:55:4c:fd 192.0.2.2 c1 ff:6f:c3:ab:c5:00:02:00:00:ab:11:f8:5c:3d:73:db:b2:6a:06" > "${LXD_DIR}/networks/${brName}/dnsmasq.leases"
  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true
  lxc config device set "${ctPrefix}A" eth0 security.ipv4_filtering true
  lxc start "${ctPrefix}A"

  if ! grep "192.0.2.3" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A" ; then
    echo "dnsmasq host config doesnt contain sequentially allocated static IPv4 config"
    false
  fi

  # Simulate changing DHCPv4 ranges.
  lxc stop -f "${ctPrefix}A"
  lxc network set "${brName}" ipv4.dhcp.ranges "192.0.2.100-192.0.2.110"
  lxc start "${ctPrefix}A"

  if ! grep "192.0.2.100" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A" ; then
    echo "dnsmasq host config doesnt contain sequentially range allocated static IPv4 config"
    false
  fi

  # Make sure br_netfilter is loaded, needed for IPv6 filtering.
  modprobe br_netfilter || true
  if ! grep 1 /proc/sys/net/bridge/bridge-nf-call-ip6tables ; then
    echo "br_netfilter didn't load, skipping IPv6 filter checks"
    lxc delete -f "${ctPrefix}A"
    lxc delete -f "${ctPrefix}B"
    lxc network delete "${brName}"
    lxc profile delete "${ctPrefix}"
    return
  fi

  # Add a fake IPv6 and check connectivity
  lxc exec "${ctPrefix}B" -- ip -6 a add 2001:db8::3/64 dev eth0
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8::254 dev eth0
  sleep 2 # Wait for DAD.
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W1 2001:db8::1
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W1 2001:db8::3

  # Enable IPv6 filtering on CT A and test (disable security.mac_filtering to check its applied too).
  lxc config device set "${ctPrefix}A" eth0 ipv6.address 2001:db8::2
  lxc config device set "${ctPrefix}A" eth0 security.mac_filtering false
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering true

  # Check MAC filter is present in ebtables.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  if ! ebtables -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
      echo "MAC filter not applied as part of ipv6_filtering in ebtables"
      false
  fi

  # Check MAC filter is present in ip6tables.
  macHex=$(echo "${ctAMAC}" |sed "s/://g")
  if ! ip6tables -S -w -t filter | grep -e "${macHex}" ; then
      echo "MAC filter not applied as part of ipv6_filtering in ip6tables"
      false
  fi

  # Check IPv6 filter is present in ebtables.
  if ! ebtables -L --Lmac2 --Lx | grep -e "2001:db8::2" ; then
       echo "IPv6 filter not applied as part of ipv6_filtering in ebtables"
      false
  fi

  # Check IPv6 filter is present in ip6tables.
  if ! ip6tables -S -w -t filter | grep -e "20010db8000000000000000000000002" ; then
      echo "IPv6 filter not applied as part of ipv6_filtering in ip6tables"
      false
  fi

  # Check DHCPv6 allocation still works (if udhcpc6 is in busybox image).
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up

  busyboxUdhcpc6=$(lxc exec "${ctPrefix}A" -- busybox --list | grep udhcpc6)
  if [ "${busyboxUdhcpc6}" = "udhcpc6" ]; then
      lxc exec "${ctPrefix}A" -- udhcpc6 -i eth0 -n
  fi

  lxc exec "${ctPrefix}A" -- ip -6 a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8::2/64 dev eth0
  sleep 2 # Wait for DAD.

  # Check basic connectivity with IPv6 filtering and real IPs configured.
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W1 2001:db8::2
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W1 2001:db8::3

  # Add a fake IP
  lxc exec "${ctPrefix}A" -- ip -6 a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8::254/64 dev eth0

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping6 -c2 -W1 2001:db8::2; then
      echo "IPv6 filter not working to host"
      false
  fi

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping6 -c2 -W1 2001:db8::3; then
      echo "IPv6 filter not working to other container"
      false
  fi

  # Stop CT A and check filters are cleaned up.
  lxc stop -f "${ctPrefix}A"
  if ebtables -L --Lmac2 --Lx | grep -e "${ctAHost}" ; then
      echo "IP filter still applied as part of ipv6_filtering in ebtables"
      false
  fi

  # Set static MAC so that SLAAC address is derived predictably and check it is applied to static config.
  lxc config device unset "${ctPrefix}A" eth0 ipv6.address
  lxc config device set "${ctPrefix}A" eth0 hwaddr 00:16:3e:92:f3:c1
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering false
  rm "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A"
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering true
  lxc start "${ctPrefix}A"
  if ! grep "\\[2001:db8::216:3eff:fe92:f3c1\\]" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A" ; then
    echo "dnsmasq host config doesnt contain dynamically allocated static IPv6 config"
    false
  fi

  lxc stop -f "${ctPrefix}A"
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering false
  rm "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A"

  # Simulate SLAAC 2001:db8::216:3eff:fe92:f3c1 being used by another container, next free IP is 2001:db8::2
  kill "$(cat "${LXD_DIR}"/networks/"${brName}"/dnsmasq.pid)"
  echo "$(date --date="1hour" +%s) 1875094469 2001:db8::216:3eff:fe92:f3c1 c1 00:02:00:00:ab:11:f8:5c:3d:73:db:b2:6a:06" > "${LXD_DIR}/networks/${brName}/dnsmasq.leases"
  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering true
  lxc start "${ctPrefix}A"
  if ! grep "\\[2001:db8::2\\]" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A" ; then
    echo "dnsmasq host config doesnt contain sequentially allocated static IPv6 config"
    false
  fi

  lxc stop -f "${ctPrefix}A"
  lxc stop -f "${ctPrefix}B"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  lxc delete -f "${ctPrefix}A"
  lxc delete -f "${ctPrefix}B"
  lxc network delete "${brName}"
  lxc profile delete "${ctPrefix}"
}
