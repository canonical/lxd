test_container_devices_nic_ipvlan() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  if ! lxc info | grep 'network_ipvlan: "true"' ; then
    echo "==> SKIP: No IPVLAN support"
    return
  fi

  ctName="nt$$"
  ipRand=$(shuf -i 0-9 -n 1)

  # Test ipvlan support to offline container (hot plugging not supported).
  ip link add "${ctName}" type dummy

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Check that starting IPVLAN container.
  sysctl net.ipv6.conf."${ctName}".proxy_ndp=1
  sysctl net.ipv6.conf."${ctName}".forwarding=1
  sysctl net.ipv4.conf."${ctName}".forwarding=1
  lxc init testimage "${ctName}"
  lxc config device add "${ctName}" eth0 nic \
    nictype=ipvlan \
    parent=${ctName} \
    ipv4.address="192.0.2.1${ipRand}" \
    ipv6.address="2001:db8::1${ipRand}" \
    mtu=1400
  lxc start "${ctName}"

  # Check custom MTU is applied.
  if ! lxc exec "${ctName}" -- ip link show eth0 | grep "mtu 1400" ; then
    echo "mtu invalid"
    false
  fi

  lxc stop "${ctName}"

  # Check that MTU is inherited from parent device when not specified on device.
  ip link set "${ctName}" mtu 1405
  lxc config device unset "${ctName}" eth0 mtu
  lxc start "${ctName}"
  if ! lxc exec "${ctName}" -- grep "1405" /sys/class/net/eth0/mtu ; then
    echo "mtu not inherited from parent"
    false
  fi

  #Spin up another container with multiple IPs.
  lxc init testimage "${ctName}2"
  lxc config device add "${ctName}2" eth0 nic \
    nictype=ipvlan \
    parent=${ctName} \
    ipv4.address="192.0.2.2${ipRand}, 192.0.2.3${ipRand}" \
    ipv6.address="2001:db8::2${ipRand}, 2001:db8::3${ipRand}"
  lxc start "${ctName}2"

  # Check comms between containers.
  lxc exec "${ctName}" -- ping -c2 -W1 "192.0.2.2${ipRand}"
  lxc exec "${ctName}" -- ping -c2 -W1 "192.0.2.3${ipRand}"
  lxc exec "${ctName}" -- ping6 -c2 -W1 "2001:db8::2${ipRand}"
  lxc exec "${ctName}" -- ping6 -c2 -W1 "2001:db8::3${ipRand}"
  lxc exec "${ctName}2" -- ping -c2 -W1 "192.0.2.1${ipRand}"
  lxc exec "${ctName}2" -- ping6 -c2 -W1 "2001:db8::1${ipRand}"
  lxc stop -f "${ctName}2"

  # Check IPVLAN ontop of VLAN parent.
  lxc stop -f "${ctName}"
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

  # Cleanup ipvlan checks
  lxc delete "${ctName}" -f
  lxc delete "${ctName}2" -f
  ip link delete "${ctName}" type dummy
}
