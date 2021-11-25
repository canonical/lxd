test_container_devices_nic_routed() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  if ! lxc info | grep 'network_veth_router: "true"' ; then
    echo "==> SKIP: No veth router support"
    return
  fi

  ctName="nt$$"
  ipRand=$(shuf -i 0-9 -n 1)

  # These special values are needed to be enabled in kernel.
  sysctl net.ipv6.conf.all.forwarding=1
  sysctl net.ipv6.conf.all.proxy_ndp=1

  # Test routed support to offline container (hot plugging not supported).
  ip link add "${ctName}" type dummy
  sysctl net.ipv6.conf."${ctName}".proxy_ndp=1
  sysctl net.ipv6.conf."${ctName}".forwarding=1
  sysctl net.ipv4.conf."${ctName}".forwarding=1
  sysctl net.ipv6.conf."${ctName}".accept_dad=0

  # Add IP addresses to parent interface (this is needed for automatic gateway detection in container).
  ip link set "${ctName}" up
  ip addr add 192.0.2.1/32 dev "${ctName}"
  ip addr add 2001:db8::1/128 dev "${ctName}"

  # Wait for IPv6 DAD to complete.
  while true
  do
    if ! ip -6 a show dev "${ctName}" | grep "tentative" ; then
      break
    fi

    sleep 0.5
  done

  # Create dummy vlan parent.
  # Use slash notation when setting sysctls on vlan interface (that has period in interface name).
  ip link add link "${ctName}" name "${ctName}.1234" type vlan id 1234
  sysctl net/ipv6/conf/"${ctName}.1234"/proxy_ndp=1
  sysctl net/ipv6/conf/"${ctName}.1234"/forwarding=1
  sysctl net/ipv4/conf/"${ctName}.1234"/forwarding=1
  sysctl net/ipv6/conf/"${ctName}.1234"/accept_dad=0

  # Add IP addresses to parent interface (this is needed for automatic gateway detection in container).
  ip link set "${ctName}.1234" up
  ip addr add 192.0.3.254/32 dev "${ctName}.1234"
  ip addr add 2001:db8:2::1/128 dev "${ctName}.1234"

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Check that starting routed container.
  lxc init testimage "${ctName}"
  lxc config device add "${ctName}" eth0 nic \
    name=eth0 \
    nictype=routed \
    parent=${ctName} \
    ipv4.address="192.0.2.1${ipRand}" \
    ipv6.address="2001:db8::1${ipRand}" \
    mtu=1600
  lxc start "${ctName}"

  # Check IP is assigned and doesn't have a broadcast address set.
  lxc exec "${ctName}" -- ip a | grep "inet 192.0.2.1${ipRand}/32 scope global eth0"

  # Check neighbour proxy entries added to parent interface.
  ip neigh show proxy dev "${ctName}" | grep "192.0.2.1${ipRand}"
  ip neigh show proxy dev "${ctName}" | grep "2001:db8::1${ipRand}"

  # Check custom MTU is applied.
  if ! lxc exec "${ctName}" -- ip link show eth0 | grep "mtu 1600" ; then
    echo "mtu invalid"
    false
  fi

  # Check MAC address is applied.
  ctMAC=$(lxc config get "${ctName}" volatile.eth0.hwaddr)
  if ! lxc exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  lxc stop "${ctName}" --force

  # Check neighbour proxy entries removed from parent interface.
  ! ip neigh show proxy dev "${ctName}" | grep "192.0.2.1${ipRand}" || false
  ! ip neigh show proxy dev "${ctName}" | grep "2001:db8::1${ipRand}" || false

  # Check that MTU is inherited from parent device when not specified on device.
  ip link set "${ctName}" mtu 1605
  lxc config device unset "${ctName}" eth0 mtu
  lxc start "${ctName}"
  lxc exec "${ctName}" -- sysctl net.ipv6.conf.eth0.accept_dad=0

  if ! lxc exec "${ctName}" -- grep "1605" /sys/class/net/eth0/mtu ; then
    echo "mtu not inherited from parent"
    false
  fi

  #Spin up another container with multiple IPs.
  lxc init testimage "${ctName}2"
  lxc config device add "${ctName}2" eth0 nic \
    name=eth0 \
    nictype=routed \
    parent=${ctName} \
    ipv4.address="192.0.2.2${ipRand}, 192.0.2.3${ipRand}" \
    ipv6.address="2001:db8::2${ipRand}, 2001:db8::3${ipRand}"
  lxc start "${ctName}2"
  lxc exec "${ctName}2" -- sysctl net.ipv6.conf.eth0.accept_dad=0

  # Wait for IPv6 DAD to complete.
  while true
  do
    if ! lxc exec "${ctName}" -- ip -6 a show dev eth0 | grep "tentative" ; then
      break
    fi

    sleep 0.5
  done

  while true
  do
    if ! lxc exec "${ctName}2" -- ip -6 a show dev eth0 | grep "tentative" ; then
      break
    fi

    sleep 0.5
  done

  # Check comms between containers.
  lxc exec "${ctName}" -- ping -c2 -W5 "192.0.2.1"
  lxc exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::1"

  lxc exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1"
  lxc exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1"

  lxc exec "${ctName}" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  lxc exec "${ctName}" -- ping -c2 -W5 "192.0.2.3${ipRand}"

  lxc exec "${ctName}" -- ping6 -c3 -W5 "2001:db8::3${ipRand}"
  lxc exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"

  lxc exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1${ipRand}"
  lxc exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1${ipRand}"

  lxc stop -f "${ctName}2"
  lxc stop -f "${ctName}"

  # Check routed ontop of VLAN parent.
  lxc config device set "${ctName}" eth0 vlan 1234
  lxc start "${ctName}"

  # Check VLAN interface created
  if ! grep "1" "/sys/class/net/${ctName}.1234/carrier" ; then
    echo "vlan interface not created"
    false
  fi

  # Check volatile cleanup on stop.
  lxc stop -f "${ctName}"
  if lxc config show "${ctName}" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr | grep -v volatile.eth0.name ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Check parent device is still up.
  if ! grep "1" "/sys/class/net/${ctName}/carrier" ; then
    echo "parent is down"
    false
  fi

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup routed checks
  lxc delete "${ctName}" -f
  lxc delete "${ctName}2" -f
  ip link delete "${ctName}.1234"
  ip link delete "${ctName}"
}
