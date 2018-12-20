test_network() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init testimage nettest

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
  lxc query -X PATCH -d "{\\\"config\\\": {\\\"ipv6.dhcp.stateful\\\": \\\"true\\\"}}" lxd/1.0/networks/lxdt$$
  [ "$(lxc network get lxdt$$ ipv6.dhcp.stateful)" = "true" ]

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

  # Configured bridge with static assignment
  lxc network create lxdt$$ dns.domain=test dns.mode=managed
  lxc network attach lxdt$$ nettest eth0
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(lxc network get lxdt$$ ipv6.address | cut -d/ -f1)00"
  lxc config device set nettest eth0 ipv4.address "${v4_addr}"
  lxc config device set nettest eth0 ipv6.address "${v6_addr}"
  grep -q "${v4_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts/nettest"
  grep -q "${v6_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts/nettest"
  lxc start nettest

  lxc network list-leases lxdt$$ | grep STATIC | grep -q "${v4_addr}"
  lxc network list-leases lxdt$$ | grep STATIC | grep -q "${v6_addr}"

  SUCCESS=0
  # shellcheck disable=SC2034
  for i in $(seq 10); do
    lxc info nettest | grep -q fd42 && SUCCESS=1 && break
    sleep 0.5
  done

  [ "${SUCCESS}" = "0" ] && (echo "Container static IP wasn't applied" && false)

  lxc delete nettest -f
  lxc network delete lxdt$$
}
