test_container_devices_nic_ipvlan() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

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
    parent="${ctName}" \
    ipv4.address="192.0.2.1${ipRand}" \
    ipv6.address="2001:db8::1${ipRand}" \
    ipv4.gateway=auto \
    ipv6.gateway=auto \
    mtu=1400
  lxc start "${ctName}"

  # Check custom MTU is applied.
  if ! lxc exec "${ctName}" -- ip link show eth0 | grep -F "mtu 1400" ; then
    echo "mtu invalid"
    false
  fi

  lxc stop "${ctName}" --force

  # Check that MTU is inherited from parent device when not specified on device.
  ip link set "${ctName}" mtu 1405
  lxc config device unset "${ctName}" eth0 mtu
  lxc start "${ctName}"
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1405" ]; then
    echo "mtu not inherited from parent"
    false
  fi

  #Spin up another container with multiple IPs.
  lxc init testimage "${ctName}2"
  lxc config device add "${ctName}2" eth0 nic \
    nictype=ipvlan \
    parent="${ctName}" \
    ipv4.address="192.0.2.2${ipRand}, 192.0.2.3${ipRand}" \
    ipv6.address="2001:db8::2${ipRand}, 2001:db8::3${ipRand}"
  lxc start "${ctName}2"

  wait_for_dad "${ctName}" eth0
  wait_for_dad "${ctName}2" eth0

  # Check comms between containers.
  lxc exec "${ctName}" -- ping -nc2 -i0.1 -W1 "192.0.2.2${ipRand}"
  lxc exec "${ctName}" -- ping -nc2 -i0.1 -W1 "192.0.2.3${ipRand}"
  lxc exec "${ctName}" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::2${ipRand}"
  lxc exec "${ctName}" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::3${ipRand}"
  lxc exec "${ctName}2" -- ping -nc2 -i0.1 -W1 "192.0.2.1${ipRand}"
  lxc exec "${ctName}2" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::1${ipRand}"
  lxc stop -f "${ctName}2"

  # Check IPVLAN ontop of VLAN parent with custom routing tables.
  lxc stop -f "${ctName}"
  lxc config device set "${ctName}" eth0 vlan 1234
  lxc config device set "${ctName}" eth0 ipv4.host_table=100
  lxc config device set "${ctName}" eth0 ipv6.host_table=101

  # Check gateway settings don't accept IPs in default l3s mode.
  ! lxc config device set "${ctName}" eth0 ipv4.gateway=192.0.2.254 || false
  ! lxc config device set "${ctName}" eth0 ipv6.gateway=2001:db8::FFFF || false

  lxc start "${ctName}"

  # Check VLAN interface created
  if [ "$(cat "/sys/class/net/${ctName}.1234/carrier")" != "1" ]; then
    echo "vlan interface not created"
    false
  fi

  # Check static routes added to custom routing table
  ip -4 route show table 100 | grep -F "192.0.2.1${ipRand}"
  ip -6 route show table 101 | grep -F "2001:db8::1${ipRand}"

  # Check volatile cleanup on stop.
  lxc stop -f "${ctName}"
  if [ "$(lxc config show "${ctName}" | grep -F volatile.eth0 | grep -vF volatile.eth0.hwaddr | grep -vF volatile.eth0.name)" != "" ]; then
    echo "unexpected volatile key remains"
    false
  fi

  # Check parent device is still up.
  if [ "$(cat "/sys/class/net/${ctName}/carrier")" != "1" ]; then
    echo "parent is down"
    false
  fi

  # Check static routes are removed from custom routing table
  ! ip -4 route show table 100 | grep -F "192.0.2.1${ipRand}" || false
  ! ip -6 route show table 101 | grep -F "2001:db8::1${ipRand}" || false

  # Check ipvlan l2 mode with mixture of singular and CIDR IPs, and gateway IPs.
  lxc config device remove "${ctName}" eth0
  lxc config device add "${ctName}" eth0 nic \
    nictype=ipvlan \
    mode=l2 \
    parent="${ctName}" \
    ipv4.address="192.0.2.1${ipRand},192.0.2.2${ipRand}/32" \
    ipv6.address="2001:db8::1${ipRand},2001:db8::2${ipRand}/128" \
    ipv4.gateway=192.0.2.254 \
    ipv6.gateway=2001:db8::FFFF \
    mtu=1400
  lxc start "${ctName}"

  lxc config device remove "${ctName}2" eth0
  lxc config device add "${ctName}2" eth0 nic \
    nictype=ipvlan \
    parent="${ctName}" \
    ipv4.address="192.0.2.3${ipRand}" \
    ipv6.address="2001:db8::3${ipRand}" \
    mtu=1400
  lxc start "${ctName}2"

  # Add an internally configured address (only possible in l2 mode).
  lxc exec "${ctName}2" -- ip -4 addr add "192.0.2.4${ipRand}/32" dev eth0
  lxc exec "${ctName}2" -- ip -6 addr add "2001:db8::4${ipRand}/128" dev eth0

  wait_for_dad "${ctName}" eth0
  wait_for_dad "${ctName}2" eth0

  # Check comms between containers.
  lxc exec "${ctName}" -- ping -nc2 -i0.1 -W1 "192.0.2.3${ipRand}"
  lxc exec "${ctName}" -- ping -nc2 -i0.1 -W1 "192.0.2.4${ipRand}"
  lxc exec "${ctName}" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::3${ipRand}"
  lxc exec "${ctName}" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::4${ipRand}"
  lxc exec "${ctName}2" -- ping -nc2 -i0.1 -W1 "192.0.2.1${ipRand}"
  lxc exec "${ctName}2" -- ping -nc2 -i0.1 -W1 "192.0.2.2${ipRand}"
  lxc exec "${ctName}2" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::1${ipRand}"
  lxc exec "${ctName}2" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::2${ipRand}"

  lxc stop -f "${ctName}"
  lxc stop -f "${ctName}2"

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
