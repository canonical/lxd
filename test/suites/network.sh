test_network() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init testimage nettest

  # Test DNS resolution of instance names
  lxc network create lxdt$$
  lxc launch testimage 0abc -n lxdt$$
  lxc launch testimage def0 -n lxdt$$
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)"
  sleep 2
  dig @"${v4_addr}" 0abc.lxd
  dig @"${v4_addr}" def0.lxd
  lxc delete -f 0abc
  lxc delete -f def0
  lxc network delete lxdt$$

  # Standard bridge with random subnet and a bunch of options
  lxc network create lxdt$$
  lxc network set lxdt$$ dns.mode dynamic
  lxc network set lxdt$$ dns.domain blah
  lxc network set lxdt$$ ipv4.routing false
  lxc network set lxdt$$ ipv6.routing false
  lxc network set lxdt$$ ipv6.dhcp.stateful true
  lxc network set lxdt$$ bridge.hwaddr 00:11:22:33:44:55
  [ "$(cat /sys/class/net/lxdt$$/address)" = "00:11:22:33:44:55" ]

  # validate unset and patch
  [ "$(lxc network get lxdt$$ ipv6.dhcp.stateful)" = "true" ]
  lxc network unset lxdt$$ ipv6.dhcp.stateful
  [ "$(lxc network get lxdt$$ ipv6.dhcp.stateful)" = "" ]
  lxc query -X PATCH -d "{\\\"config\\\": {\\\"ipv6.dhcp.stateful\\\": \\\"true\\\"}}" /1.0/networks/lxdt$$
  [ "$(lxc network get lxdt$$ ipv6.dhcp.stateful)" = "true" ]

  # check ipv4.address and ipv6.address can be unset without triggering random subnet generation.
  lxc network unset lxdt$$ ipv4.address
  ! lxc network show lxdt$$ | grep ipv4.address || false
  lxc network unset lxdt$$ ipv6.address
  ! lxc network show lxdt$$ | grep ipv6.address || false

  # check ipv4.address and ipv6.address can be regenerated on update using "auto" value.
  lxc network set lxdt$$ ipv4.address auto
  lxc network show lxdt$$ | grep ipv4.address
  lxc network set lxdt$$ ipv6.address auto
  lxc network show lxdt$$ | grep ipv6.address

  # delete the network
  lxc network delete lxdt$$

  # edit network description
  lxc network create lxdt$$
  lxc network show lxdt$$ | sed 's/^description:.*/description: foo/' | lxc network edit lxdt$$
  lxc network show lxdt$$ | grep -q 'description: foo'
  lxc network delete lxdt$$

  # rename network
  lxc network create lxdt$$
  lxc network rename lxdt$$ newnet$$
  lxc network list | grep -qv lxdt$$  # the old name is gone
  lxc network delete newnet$$

  # Unconfigured bridge
  lxc network create lxdt$$ ipv4.address=none ipv6.address=none
  lxc network delete lxdt$$

  # Check that we can return state for physical networks
  ip link add dummy0 type dummy
  lxc network create lxdt$$ --type=physical parent=dummy0

  expected_state=$(lxc network info dummy0 | grep -F "State:")
  expected_type=$(lxc network info dummy0 | grep -F "Type:")
  lxc network info lxdt$$ | grep -qF "${expected_state}"
  lxc network info lxdt$$ | grep -qF "${expected_type}"

  # Delete physical network and check for expected response
  ip link delete dummy0
  lxc network info lxdt$$ | grep -qF "State: unavailable"
  lxc network info lxdt$$ | grep -qF "Type: unknown"

  lxc network delete lxdt$$

  # Configured bridge with static assignment
  lxc network create lxdt$$ dns.domain=test dns.mode=managed ipv6.dhcp.stateful=true
  lxc network attach lxdt$$ nettest eth0
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(lxc network get lxdt$$ ipv6.address | cut -d/ -f1)00"
  lxc config device set nettest eth0 ipv4.address "${v4_addr}"
  lxc config device set nettest eth0 ipv6.address "${v6_addr}"
  grep -q "${v4_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts/nettest.eth0"
  grep -q "${v6_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts/nettest.eth0"
  lxc start nettest

  lxc network list-leases lxdt$$ | grep STATIC | grep -q "${v4_addr}"
  lxc network list-leases lxdt$$ | grep STATIC | grep -q "${v6_addr}"

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! lxc exec nettest -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
    lxc exec nettest -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Check IPAM information
  net_ipv4="$(lxc network get lxdt$$ ipv4.address)"
  net_ipv6="$(lxc network get lxdt$$ ipv6.address)"

  lxc network list-allocations | grep -e "${net_ipv4}" -e "${net_ipv6}"
  lxc network list-allocations | grep -e "/1.0/networks/lxdt$$" -e "/1.0/instances/nettest"
  lxc network list-allocations | grep -e "${v4_addr}" -e "${v6_addr}"
  lxc network list-allocations localhost: | grep -e "${net_ipv4}" -e "${net_ipv6}"
  lxc network list-allocations localhost: | grep -e "/1.0/networks/lxdt$$" -e "/1.0/instances/nettest"
  lxc network list-allocations localhost: | grep -e "${v4_addr}" -e "${v6_addr}"

  lxc delete nettest -f
  lxc network delete lxdt$$
}
