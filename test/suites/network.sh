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
  ipam_output="$(lxc network list-allocations)"

  net_ipv4="$(lxc network get lxdt$$ ipv4.address)"
  net_ipv6="$(lxc network get lxdt$$ ipv6.address)"

  expected_addresses_net="[\"${net_ipv4}\",\"${net_ipv6}\"]"
  expected_used_by_net="\"/1.0/networks/lxdt$$\""
  expected_nat_net='false'

  expected_addresses_instance="[\"${v4_addr}\",\"${v6_addr}\"]"
  expected_used_by_instance='"/1.0/instances/nettest"'
  expected_nat_instance='false'

  echo "$ipam_output" | jq -c 'with_entries(.)' | while read -r object; do
    type=$(echo "$object" | jq -r '.type')

    if [ "$type" = 'network' ]; then
      addresses=$(echo "$object" | jq -c '.addresses')
      used_by=$(echo "$object" | jq '.used_by')
      nat=$(echo "$object" | jq '.nat')

      # Check if the values are as expected for type "network"
      if [ "$addresses" != "$expected_addresses_net" ] || [ "$used_by" != "$expected_used_by_net" ] || [ "$nat" != "$expected_nat_net" ]; then
        echo "The JSON object fields for type 'network' are not as expected."
        false
      fi
    elif [ "$type" = 'instance' ]; then
      used_by=$(echo "$object" | jq '.used_by')
      addresses=$(echo "$object" | jq -c '.addresses')
      base_ipv4=$(echo "$addresses" | jq -r '.[0]' | cut -d '/' -f1)
      base_ipv6=$(echo "$addresses" | jq -r '.[1]' | cut -d '/' -f1)
      no_cidr_instance_addresses="[\"${base_ipv4}\",\"${base_ipv6}\"]"

      if [ "$no_cidr_instance_addresses" != "$expected_addresses_instance" ] || [ "$used_by" != "$expected_used_by_instance" ] || [ "$nat" != "$expected_nat_instance" ]; then
        echo "The JSON object fields for type 'instance' are not as expected."
        false
      fi
    else
      echo "Unknown type: $type"
    fi
  done

  lxc delete nettest -f
  lxc network delete lxdt$$
}
