test_container_devices_nic_bridged() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  vethHostName="veth$$"
  ctName="nt$$"
  ctMAC="0A:92:a7:0d:b7:D9"
  ipRand=$(shuf -i 0-9 -n 1)
  brName="lxdt$$"

  # Standard bridge with random subnet and a bunch of options
  lxc network create "${brName}"
  lxc network set "${brName}" dns.mode managed
  lxc network set "${brName}" dns.domain blah
  lxc network set "${brName}" ipv4.nat true
  lxc network set "${brName}" ipv4.routing false
  lxc network set "${brName}" ipv6.routing false
  lxc network set "${brName}" ipv6.dhcp.stateful true
  lxc network set "${brName}" bridge.hwaddr 00:11:22:33:44:55
  lxc network set "${brName}" ipv4.address 192.0.2.1/24
  lxc network set "${brName}" ipv6.address 2001:db8::1/64
  lxc network set "${brName}" ipv4.routes 192.0.3.0/24
  lxc network set "${brName}" ipv6.routes 2001:db8::3:0/64
  [ "$(cat /sys/class/net/${brName}/address)" = "00:11:22:33:44:55" ]

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Test pre-launch profile config is applied at launch
  lxc profile copy default "${ctName}"

  # Modifiy profile nictype and parent in atomic operation to ensure validation passes.
  lxc profile show "${ctName}" | sed  "s/nictype: p2p/nictype: bridged\n    parent: ${brName}/" | lxc profile edit "${ctName}"

  lxc profile device set "${ctName}" eth0 ipv4.routes "192.0.2.1${ipRand}/32"
  lxc profile device set "${ctName}" eth0 ipv6.routes "2001:db8::1${ipRand}/128"
  lxc profile device set "${ctName}" eth0 limits.ingress 1Mbit
  lxc profile device set "${ctName}" eth0 limits.egress 2Mbit
  lxc profile device set "${ctName}" eth0 host_name "${vethHostName}"
  lxc profile device set "${ctName}" eth0 mtu "1400"
  lxc profile device set "${ctName}" eth0 hwaddr "${ctMAC}"
  lxc launch testimage "${ctName}" -p "${ctName}"

  # Check profile routes are applied on boot.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check profile limits are applied on boot.
  if ! tc class show dev "${vethHostName}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied in container on boot.
  if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check profile custom MAC is applied in container on boot.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Add IP alias to container and check routes actually work.
  lxc exec "${ctName}" -- ip -4 addr add "192.0.2.1${ipRand}/32" dev eth0
  lxc exec "${ctName}" -- ip -4 route add default dev eth0
  ping -c2 -W1 "192.0.2.1${ipRand}"
  lxc exec "${ctName}" -- ip -6 addr add "2001:db8::1${ipRand}/128" dev eth0
  sleep 2 #Wait for link local gateway advert.
  ping6 -c2 -W1 "2001:db8::1${ipRand}"

  # Test hot plugging a container nic with different settings to profile with the same name.
  lxc config device add "${ctName}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${brName} \
    ipv4.routes="192.0.2.2${ipRand}/32" \
    ipv6.routes="2001:db8::2${ipRand}/128" \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    host_name="${vethHostName}" \
    hwaddr="${ctMAC}" \
    mtu=1401

  # Check profile routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check routes are applied on hot-plug.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check limits are applied on hot-plug.
  if ! tc class show dev "${vethHostName}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied on hot-plug.
  if ! lxc exec "${ctName}" -- grep "1401" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check custom MAC is applied on hot-plug.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Test removing hot plugged device and check profile nic is restored.
  lxc config device remove "${ctName}" eth0

  # Check routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check profile routes are applied on hot-removal.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check profile limits are applie on hot-removal.
  if ! tc class show dev "${vethHostName}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied on hot-removal.
  if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Test hot plugging a container nic then updating it.
  lxc config device add "${ctName}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${brName} \
    host_name="${vethHostName}" \
    ipv4.routes="192.0.2.1${ipRand}/32" \
    ipv6.routes="2001:db8::1${ipRand}/128"

  # Check removing a required option fails.
  if lxc config device unset "${ctName}" eth0 parent ; then
    echo "shouldnt be able to unset invalrequiredid option"
    false
  fi

  # Check updating an invalid option fails.
  if lxc config device set "${ctName}" eth0 invalid.option "invalid value" ; then
    echo "shouldnt be able to set invalid option"
    false
  fi

  # Check setting invalid IPv4 route.
  if lxc config device set "${ctName}" eth0 ipv4.routes "192.0.2.1/33" ; then
      echo "shouldnt be able to set invalid ipv4.routes value"
    false
  fi

  # Check setting invalid IPv6 route.
  if lxc config device set "${ctName}" eth0 ipv6.routes "2001:db8::1/129" ; then
      echo "shouldnt be able to set invalid ipv6.routes value"
    false
  fi

  lxc config device set "${ctName}" eth0 ipv4.routes "192.0.2.2${ipRand}/32"
  lxc config device set "${ctName}" eth0 ipv6.routes "2001:db8::2${ipRand}/128"
  lxc config device set "${ctName}" eth0 limits.ingress 3Mbit
  lxc config device set "${ctName}" eth0 limits.egress 4Mbit
  lxc config device set "${ctName}" eth0 mtu 1402
  lxc config device set "${ctName}" eth0 hwaddr "${ctMAC}"

  # Check original routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check limits are applied on update.
  if ! tc class show dev "${vethHostName}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied update.
  if ! lxc exec "${ctName}" -- grep "1402" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check custom MAC is applied update.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi


  # Add an external 3rd party route to the bridge interface and check that it and the container
  # routes remain when the network is reconfigured.
  ip -4 route add 192.0.2"${ipRand}".0/24 via 192.0.2.1"${ipRand}" dev "${brName}"

  # Now change something that will trigger a network restart
  lxc network set "${brName}" ipv4.nat false

  # Check external routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2${ipRand}.0/24 via 192.0.2.1${ipRand}" ; then
    echo "external ipv4 routes invalid after network update"
    false
  fi

  # Check container routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "container ipv4 routes invalid after network update"
    false
  fi

  # Check network routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.3.0/24" ; then
    echo "network ipv4 routes invalid after network update"
    false
  fi

  # Check volatile cleanup on stop.
  lxc stop -f "${ctName}"
  if lxc config show "${ctName}" | grep volatile.eth0 ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Test DHCP lease clearance.
  lxc delete "${ctName}" -f
  lxc launch testimage "${ctName}" -p "${ctName}"

  # Request DHCPv4 lease with custom name (to check managed name is allocated instead).
  lxc exec "${ctName}" -- udhcpc -i eth0 -F "${ctName}custom"

  # Check DHCPv4 lease is allocated.
  if ! grep -i "${ctMAC}" "${LXD_DIR}/networks/${brName}/dnsmasq.leases" ; then
    echo "DHCPv4 lease not allocated"
    false
  fi

  # Check DHCPv4 lease has DNS record assigned.
  if ! dig @192.0.2.1 "${ctName}.blah" | grep "${ctName}.blah.\\+0.\\+IN.\\+A.\\+192.0.2." ; then
    echo "DNS resolution of DHCP name failed"
    false
  fi

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! lxc exec "${ctName}" -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
        lxc exec "${ctName}" -- udhcpc6 -i eth0
  fi

  # Delete container, check LXD releases lease.
  lxc delete "${ctName}" -f

  # Wait for DHCP release to be processed.
  sleep 2

  # Check DHCPv4 lease is released (space before the MAC important to avoid mismatching IPv6 lease).
  if grep -i " ${ctMAC}" "${LXD_DIR}/networks/${brName}/dnsmasq.leases" ; then
    echo "DHCPv4 lease not released"
    false
  fi

  # Check DHCPv6 lease is released.
  if grep -i " ${ctName}" "${LXD_DIR}/networks/${brName}/dnsmasq.leases" ; then
    echo "DHCPv6 lease not released"
    false
  fi

  # Check dnsmasq host config file is removed.
  if [ -f "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}" ] ; then
    echo "dnsmasq host config file not removed"
    false
  fi

  # Check dnsmasq host file is updated on new device.
  lxc init testimage "${ctName}" -p "${ctName}"
  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0 ipv4.address=192.0.2.200 ipv6.address=2001:db8::200

  ls -lR "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/"

  if ! grep "192.0.2.200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}" ; then
    echo "dnsmasq host config not updated with IPv4 address"
    false
  fi

  if ! grep "2001:db8::200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}" ; then
    echo "dnsmasq host config not updated with IPv6 address"
    false
  fi

  lxc config device remove "${ctName}" eth0

  if grep "192.0.2.200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}" ; then
    echo "dnsmasq host config still has old IPv4 address"
    false
  fi

  if grep "2001:db8::200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}" ; then
    echo "dnsmasq host config still has old IPv6 address"
    false
  fi

  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0
  if [ ! -f "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}" ] ; then
    echo "dnsmasq host config file not created"
    false
  fi

  # Check connecting device to non-managed bridged.
  ip link add "${ctName}" type dummy
  lxc config device set "${ctName}" eth0 parent "${ctName}"
  if [ -f "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}" ] ; then
    echo "dnsmasq host config file not removed from old network"
    false
  fi

  ip link delete "${ctName}"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup.
  lxc delete "${ctName}" -f
  lxc network delete "${brName}"
  lxc profile delete "${ctName}"
}
