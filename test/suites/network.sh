#!/bin/sh

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
  lxc network delete lxdt$$

  # Unconfigured bridge
  lxc network create lxdt$$ ipv4.address=none ipv6.address=none
  lxc network delete lxdt$$

  # Configured bridge with static assignment
  lxc network create lxdt$$ dns.domain=test dns.mode=managed
  lxc network attach lxdt$$ nettest eth0
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)00"
  lxc config device set nettest eth0 ipv4.address "${v4_addr}"
  lxc config device set nettest eth0 ipv6.address "${v6_addr}"
  grep -q "${v4_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts"
  grep -q "${v6_addr}.*nettest" "${LXD_DIR}/networks/lxdt$$/dnsmasq.hosts"
  lxc start nettest

  SUCCESS=0
  # shellcheck disable=SC2034
  for i in $(seq 10); do
    lxc info nettest | grep -q fd42 && SUCCESS=1 && break
    sleep 1
  done

  [ "${SUCCESS}" = "0" ] && (echo "Container static IP wasn't applied" && false)

  lxc delete nettest -f
  lxc network delete lxdt$$
}
