test_container_devices_nic() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  veth_host_name="veth$$"
  ct_name="nictest$$"
  ipRand=$(shuf -i 0-9 -n 1)
  brName="lxdt$$"

  # Standard bridge with random subnet and a bunch of options
  lxc network create ${brName}
  lxc network set lxdt$$ dns.mode dynamic
  lxc network set lxdt$$ dns.domain blah
  lxc network set lxdt$$ ipv4.routing false
  lxc network set lxdt$$ ipv6.routing false
  lxc network set lxdt$$ ipv6.dhcp.stateful true
  lxc network set lxdt$$ bridge.hwaddr 00:11:22:33:44:55
  [ "$(cat /sys/class/net/lxdt$$/address)" = "00:11:22:33:44:55" ]

  # Test pre-launch profile config is applied at launch.
  lxc profile copy default ${ct_name}
  lxc profile device set ${ct_name} eth0 ipv4.routes "192.0.2.1${ipRand}/32"
  lxc profile device set ${ct_name} eth0 ipv6.routes "2001:db8::1${ipRand}/128"
  lxc profile device set ${ct_name} eth0 limits.ingress 1Mbit
  lxc profile device set ${ct_name} eth0 limits.egress 2Mbit
  lxc profile device set ${ct_name} eth0 host_name "${veth_host_name}"
  lxc launch testimage "${ct_name}" -p ${ct_name}
  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! tc class show dev "${veth_host_name}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Test hot plugging a container nic with different settings to profile with the same name.
  lxc config device add "${ct_name}" eth0 nic \
    nictype=bridged \
    parent=${brName} \
    ipv4.routes="192.0.2.2${ipRand}/32" \
    ipv6.routes="2001:db8::2${ipRand}/128" \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    host_name="${veth_host_name}"

  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! tc class show dev "${veth_host_name}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Test removing hot plugged device and check profile nic is restored.
  lxc config device remove "${ct_name}" eth0
  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! tc class show dev "${veth_host_name}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Test hot plugging a container nic then updating it.
  lxc config device add "${ct_name}" eth0 nic \
    nictype=bridged \
    parent=${brName} \
    host_name="${veth_host_name}"
  lxc config device set "${ct_name}" eth0 ipv4.routes "192.0.2.2${ipRand}/32"
  lxc config device set "${ct_name}" eth0 ipv6.routes "2001:db8::2${ipRand}/128"
  lxc config device set "${ct_name}" eth0 limits.ingress 3Mbit
  lxc config device set "${ct_name}" eth0 limits.egress 4Mbit

  if ! ip -4 r list dev "${veth_host_name}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! tc class show dev "${veth_host_name}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Test adding p2p veth to running container.
  lxc config device add "${ct_name}" eth1 nic \
    nictype=p2p \
    ipv4.routes="192.0.2.3${ipRand}/32" \
    ipv6.routes="2001:db8::3${ipRand}/128" \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    host_name="${veth_host_name}p2p"

  if ! ip -4 r list dev "${veth_host_name}p2p" | grep "192.0.2.3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${veth_host_name}p2p" | grep "2001:db8::3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! tc class show dev "${veth_host_name}p2p" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${veth_host_name}p2p" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Cleanup.
  lxc config device remove "${ct_name}" eth1
  lxc delete "${ct_name}" -f
  lxc network delete "${brName}"
  lxc profile delete "${ct_name}"

  # Test adding a p2p device to a running container without host_name and no limits/routes.
  lxc launch testimage "${ct_name}"
  lxc config device add "${ct_name}" eth1 nic \
    nictype=p2p
  lxc config device remove "${ct_name}" eth1
  lxc delete "${ct_name}" -f

  # Create dummy interface for use with macvlan.
  ip link add "${ct_name}" type dummy

  # Test adding non-veth macvlan to stopped container and then starting.
  lxc init testimage "${ct_name}"
  lxc config device add "${ct_name}" eth0 nic \
    nictype=macvlan \
    parent=${ct_name}
  lxc start "${ct_name}"

  # Test adding non-veth macvlan to running container.
  lxc config device add "${ct_name}" eth1 nic \
    nictype=macvlan \
    parent=${ct_name}

  # Test removing non-veth macvlan from running container.
  lxc config device remove "${ct_name}" eth1

  # Cleanup.
  lxc delete "${ct_name}" -f
  ip link delete "${ct_name}" type dummy
}
