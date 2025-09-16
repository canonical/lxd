test_network() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Test DNS resolution of instance names
  lxc network create lxdt$$ ipv6.address=none
  lxc launch testimage 0abc -d "${SMALL_ROOT_DISK}" -n lxdt$$
  lxc launch testimage def0 -d "${SMALL_ROOT_DISK}" -n lxdt$$
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)"
  sleep 2
  dig @"${v4_addr}" 0abc.lxd
  dig @"${v4_addr}" def0.lxd
  lxc delete -f 0abc def0
  lxc network delete lxdt$$

  # Cleanup any leftover from previous run
  ip link delete dummy0 || true

  # Check that we return bridge informatin for ovs bridges
  systemctl start openvswitch-switch
  ip link add dummy0 type dummy
  ovs-vsctl add-br ovs-br0
  ovs-vsctl add-port ovs-br0 dummy0 tag=9
  lxc network info ovs-br0 | grep -xF "Bridge:"

  # Check that we are able to return linux bridge information if ovs service is disabled
  systemctl stop openvswitch-switch
  ip link add native-br0 type bridge
  lxc network info native-br0 | grep -xF "Bridge:"

  # Cleanup
  systemctl start openvswitch-switch
  ovs-vsctl del-br ovs-br0
  ip link delete native-br0
  ip link delete dummy0

  # Standard bridge with random subnet and a bunch of options
  lxc network create lxdt$$
  lxc network set lxdt$$ dns.mode dynamic
  lxc network set lxdt$$ dns.domain blah
  lxc network set lxdt$$ ipv4.routing false
  lxc network set lxdt$$ ipv6.routing false
  lxc network set lxdt$$ ipv6.dhcp.stateful true
  lxc network set lxdt$$ bridge.hwaddr 00:11:22:33:44:55
  [ "$(< /sys/class/net/lxdt$$/address)" = "00:11:22:33:44:55" ]

  # validate unset and patch
  [ "$(lxc network get lxdt$$ ipv6.dhcp.stateful)" = "true" ]
  lxc network unset lxdt$$ ipv6.dhcp.stateful
  [ "$(lxc network get lxdt$$ ipv6.dhcp.stateful)" = "" ]
  lxc query -X PATCH -d "{\\\"config\\\": {\\\"ipv6.dhcp.stateful\\\": \\\"true\\\"}}" /1.0/networks/lxdt$$
  [ "$(lxc network get lxdt$$ ipv6.dhcp.stateful)" = "true" ]

  # check ipv4.address and ipv6.address can be unset without triggering random subnet generation.
  lxc network unset lxdt$$ ipv4.address
  [ "$(lxc network get "lxdt$$" ipv4.address)" = "" ]
  lxc network unset lxdt$$ ipv6.address
  [ "$(lxc network get "lxdt$$" ipv6.address)" = "" ]
  ! lxc network show lxdt$$ | grep -F .address || false

  # check ipv4.address and ipv6.address can be regenerated individually on update using "auto" value.
  original_ipv4_address="$(lxc network get "lxdt$$" ipv4.address)"
  original_ipv6_address="$(lxc network get "lxdt$$" ipv6.address)"
  lxc network set lxdt$$ ipv4.address=auto
  new_ipv4_address="$(lxc network get "lxdt$$" ipv4.address)"
  [ "${new_ipv4_address}" != "${original_ipv4_address}" ]
  [ "$(lxc network get lxdt$$ ipv6.address)" = "${original_ipv6_address}" ]
  lxc network set lxdt$$ ipv6.address auto
  new_ipv6_address="$(lxc network get "lxdt$$" ipv6.address)"
  [ "$(lxc network get lxdt$$ ipv4.address)" = "${new_ipv4_address}" ]
  [ "${new_ipv6_address}" != "${original_ipv6_address}" ]
  # the "auto" value is special and should not appear as it is replaced by a random address.
  ! lxc network show lxdt$$ | grep -F .address | grep -wF auto || false

  # delete the network
  lxc network delete lxdt$$

  # edit network description
  lxc network create lxdt$$ ipv4.address=none ipv6.address=none
  lxc network show lxdt$$ | sed 's/^description:.*/description: foo/' | lxc network edit lxdt$$
  [ "$(lxc network get lxdt$$ -p description)" = "foo" ]
  lxc network delete lxdt$$

  # rename network
  lxc network create lxdt$$ ipv4.address=none ipv6.address=none
  lxc network rename lxdt$$ newnet$$
  ! lxc network list | grep -wF "lxdt$$" || false # the old name is gone
  lxc network delete newnet$$

  # Check that we can return state for physical networks
  ip link add dummy0 type dummy
  lxc network create lxdt$$ --type=physical parent=dummy0

  expected_state=$(lxc network info dummy0 | grep -F "State:")
  expected_type=$(lxc network info dummy0 | grep -F "Type:")
  lxc network info lxdt$$ | grep -xF "${expected_state}"
  lxc network info lxdt$$ | grep -xF "${expected_type}"

  # Delete physical network and check for expected response
  ip link delete dummy0
  lxc network info lxdt$$ | grep -xF "State: unavailable"
  lxc network info lxdt$$ | grep -xF "Type: unknown"

  lxc network delete lxdt$$

  lxc init testimage nettest
  # Configured bridge with static assignment
  lxc network create lxdt$$ dns.domain=test dns.mode=managed ipv6.dhcp.stateful=true
  lxc network attach lxdt$$ nettest eth0
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(lxc network get lxdt$$ ipv6.address | cut -d/ -f1)00"
  lxc config device set nettest eth0 ipv4.address="${v4_addr}" ipv6.address="${v6_addr}"
  grep -q "${v4_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts/nettest.eth0"
  grep -q "${v6_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts/nettest.eth0"
  lxc start nettest

  # Create new project with an instance with ipv[46] for the next tests.
  lxc project create foo -c features.networks=false -c features.images=false -c features.profiles=false
  lxc launch testimage outsider -d "${SMALL_ROOT_DISK}" -n lxdt$$ --project foo
  v4_addr_foo="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)1"
  v6_addr_foo="$(lxc network get lxdt$$ ipv6.address | cut -d/ -f1)01"
  lxc config device set outsider eth0 ipv4.address="${v4_addr_foo}" ipv6.address="${v6_addr_foo}" --project foo

  list_leases="$(lxc network list-leases -f csv lxdt$$)"
  grep -F ",${v4_addr},STATIC" <<< "${list_leases}"
  grep -F ",${v6_addr},STATIC" <<< "${list_leases}"
  list_leases="$(lxc network list-leases -f csv lxdt$$ --project foo)"
  grep -F ",${v4_addr_foo},STATIC" <<< "${list_leases}"
  grep -F ",${v6_addr_foo},STATIC" <<< "${list_leases}"

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  if lxc exec nettest -- busybox --list | grep -wF udhcpc6 ; then
    lxc exec nettest -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep -F 'IPv6 obtained'
  fi

  # Check IPAM information
  net_ipv4="$(lxc network get lxdt$$ ipv4.address)"
  net_ipv6="$(lxc network get lxdt$$ ipv6.address)"

  list_allocations="$(lxc network list-allocations)"
  grep -F -e "${net_ipv4}" -e "${net_ipv6}" <<< "${list_allocations}"
  grep -F -e "/1.0/networks/lxdt$$" -e "/1.0/instances/nettest" <<< "${list_allocations}"
  grep -F -e "${v4_addr}" -e "${v6_addr}" <<< "${list_allocations}"
  list_allocations="$(lxc network list-allocations localhost:)"
  grep -F -e "${net_ipv4}" -e "${net_ipv6}" <<< "${list_allocations}"
  grep -F -e "/1.0/networks/lxdt$$" -e "/1.0/instances/nettest" <<< "${list_allocations}"
  grep -F -e "${v4_addr}" -e "${v6_addr}" <<< "${list_allocations}"
  list_allocations="$(lxc network list-allocations --format csv)"
  grep -F "/1.0/instances/outsider?project=foo,${v4_addr_foo}/32," <<< "${list_allocations}"
  grep -F "/1.0/instances/outsider?project=foo,${v6_addr_foo}/128," <<< "${list_allocations}"

  lxc delete -f outsider --project foo
  lxc project delete foo
  lxc delete nettest -f
  lxc network delete lxdt$$
}
