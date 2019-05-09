test_container_devices_nic_bridged() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  veth_host_name="veth$$"
  ct_name="nt$$"
  ipRand=$(shuf -i 0-9 -n 1)
  brName="lxdt$$"

  # Standard bridge with random subnet and a bunch of options
  lxc network create "${brName}"
  lxc network set "${brName}" dns.mode dynamic
  lxc network set "${brName}" dns.domain blah
  lxc network set "${brName}" ipv4.routing false
  lxc network set "${brName}" ipv6.routing false
  lxc network set "${brName}" ipv6.dhcp.stateful true
  lxc network set "${brName}" bridge.hwaddr 00:11:22:33:44:55
  [ "$(cat /sys/class/net/${brName}/address)" = "00:11:22:33:44:55" ]

  # Test pre-launch profile config is applied at launch.
  lxc profile copy default "${ct_name}"
  lxc profile device set "${ct_name}" eth0 ipv4.routes "192.0.2.1${ipRand}/32"
  lxc profile device set "${ct_name}" eth0 ipv6.routes "2001:db8::1${ipRand}/128"
  lxc profile device set "${ct_name}" eth0 limits.ingress 1Mbit
  lxc profile device set "${ct_name}" eth0 limits.egress 2Mbit
  lxc profile device set "${ct_name}" eth0 host_name "${veth_host_name}"
  lxc profile device set "${ct_name}" eth0 mtu "1400"
  lxc launch testimage "${ct_name}" -p "${ct_name}"

  # Check profile routes are applied on boot.
  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi

  # Check profile limits are applied on boot.
  if ! tc class show dev "${veth_host_name}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied on boot.
  if ! lxc exec "${ct_name}" -- ip link show eth0 | grep "mtu 1400" ; then
    echo "mtu invalid"
    false
  fi

  # Test hot plugging a container nic with different settings to profile with the same name.
  lxc config device add "${ct_name}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${brName} \
    ipv4.routes="192.0.2.2${ipRand}/32" \
    ipv6.routes="2001:db8::2${ipRand}/128" \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    host_name="${veth_host_name}" \
    mtu=1401

  # Check routes are applied on hot-plug.
  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi

  # Check limits are applied on hot-plug.
  if ! tc class show dev "${veth_host_name}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied on hot-plug.
  if ! lxc exec "${ct_name}" -- ip link show eth0 | grep "mtu 1401" ; then
    echo "mtu invalid"
    false
  fi

  # Test removing hot plugged device and check profile nic is restored.
  lxc config device remove "${ct_name}" eth0

  # Check profile routes are applied on hot-removal.
  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi

  # Check profile limits are applie on hot-removal.
  if ! tc class show dev "${veth_host_name}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied on hot-removal.
  if ! lxc exec "${ct_name}" -- ip link show eth0 | grep "mtu 1400" ; then
    echo "mtu invalid"
    false
  fi

  # Test hot plugging a container nic then updating it.
  lxc config device add "${ct_name}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${brName} \
    host_name="${veth_host_name}"

  lxc config device set "${ct_name}" eth0 ipv4.routes "192.0.2.2${ipRand}/32"
  lxc config device set "${ct_name}" eth0 ipv6.routes "2001:db8::2${ipRand}/128"
  lxc config device set "${ct_name}" eth0 limits.ingress 3Mbit
  lxc config device set "${ct_name}" eth0 limits.egress 4Mbit
  lxc config device set "${ct_name}" eth0 mtu 1402

  # Check routes are applied on update.
  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi

  # Check limits are applied on update.
  if ! tc class show dev "${veth_host_name}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied update.
  if ! lxc exec "${ct_name}" -- ip link show eth0 | grep "mtu 1402" ; then
    echo "mtu invalid"
    false
  fi

  # Cleanup.
  lxc config device remove "${ct_name}" eth0
  lxc delete "${ct_name}" -f
  lxc network delete "${brName}"
  lxc profile delete "${ct_name}"
}
