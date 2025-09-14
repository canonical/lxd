test_container_devices_nic_bridged() {
  if uname -r | grep -- -kvm$; then
    echo "==> SKIP: the -kvm kernel flavor is missing CONFIG_NET_SCH_HTB which is required for 'tc qdisc htb'"
    return
  fi

  ensure_import_testimage

  firewallDriver=$(lxc info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')

  if [ "$firewallDriver" != "xtables" ] && [ "$firewallDriver" != "nftables" ]; then
    echo "Unrecognised firewall driver: ${firewallDriver}"
    false
  fi

  vethHostName="veth$$"
  ctName="nt$$"
  ctMAC="0a:92:a7:0d:b7:d9"
  ipRand=$(shuf -i 0-9 -n 1)
  brName="lxdt$$"
  dnsDomain="blah"

  # Standard bridge with random subnet and a bunch of options
  lxc network create "${brName}"
  lxc network set "${brName}" dns.mode managed
  lxc network set "${brName}" dns.domain "${dnsDomain}"
  lxc network set "${brName}" ipv4.nat true
  lxc network set "${brName}" ipv4.routing false
  lxc network set "${brName}" ipv6.routing false
  lxc network set "${brName}" ipv4.dhcp.ranges 192.0.2.100-192.0.2.200
  lxc network set "${brName}" ipv6.dhcp.ranges 2001:db8::100-2001:db8::f00
  lxc network set "${brName}" ipv6.dhcp.stateful true
  lxc network set "${brName}" bridge.hwaddr 00:11:22:33:44:55
  lxc network set "${brName}" ipv4.address 192.0.2.1/24
  lxc network set "${brName}" ipv6.address 2001:db8::1/64
  lxc network set "${brName}" ipv4.routes 192.0.3.0/24
  lxc network set "${brName}" ipv6.routes 2001:db8::/64
  [ "$(< "/sys/class/net/${brName}/address")" = "00:11:22:33:44:55" ]

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Test device renaming without "eth0" device applied by profile.
  lxc profile copy default "${ctName}"
  lxc profile device remove "${ctName}" eth0
  lxc init --empty "${ctName}" -d "${SMALL_ROOT_DISK}" -p "${ctName}"
  lxc config device add "${ctName}" eth0 nic network="${brName}" name=eth0
  [ "$(lxc config device get "${ctName}" eth0 name)" = "eth0" ]
  [ "$(lxc config device get "${ctName}" eth0 network)" = "${brName}" ]
  lxc config show -e "${ctName}" | sed '/^devices:/,/type:/ s/eth0/eth1/' | lxc config edit "${ctName}" # Rename added "eth0" to "eth1"
  ! lxc config device get "${ctName}" eth0 name || false
  [ "$(lxc config device get "${ctName}" eth1 name)" = "eth1" ]
  [ "$(lxc config device get "${ctName}" eth1 network)" = "${brName}" ]
  # Cleanup for remaining tests.
  lxc delete "${ctName}" -f
  lxc profile delete "${ctName}"

  echo "Test pre-launch profile config is applied at launch"

  # Create profile for new containers by atomically modifying nictype and parent to ensure validation passes.
  lxc profile show default | sed  "s/nictype: p2p/nictype: bridged\\n    parent: ${brName}/" | lxc profile create "${ctName}"

  lxc profile device set "${ctName}" eth0 ipv4.routes "192.0.2.1${ipRand}/32"
  lxc profile device set "${ctName}" eth0 ipv6.routes "2001:db8::1${ipRand}/128"
  lxc profile device set "${ctName}" eth0 limits.ingress 1Mbit
  lxc profile device set "${ctName}" eth0 limits.egress 2Mbit
  lxc profile device set "${ctName}" eth0 limits.priority 5
  lxc profile device set "${ctName}" eth0 host_name "${vethHostName}"
  lxc profile device set "${ctName}" eth0 mtu "1400"
  lxc profile device set "${ctName}" eth0 queue.tx.length "1200"
  lxc profile device set "${ctName}" eth0 hwaddr "${ctMAC}"

  lxc init testimage "${ctName}" -d "${SMALL_ROOT_DISK}" -p "${ctName}"

  # Check that adding another NIC to the same network fails because it triggers duplicate instance DNS name checks.
  # Because this would effectively cause 2 NICs with the same instance name to be connected to the same network.
  ! lxc config device add "${ctName}" eth1 nic network="${brName}" || false

  # Test device name validation (use vlan=1 to avoid trigger instance DNS name conflict detection).
  lxc config device add "${ctName}" 127.0.0.1 nic network="${brName}" vlan=1
  lxc config device remove "${ctName}" 127.0.0.1
  lxc config device add "${ctName}" ::1 nic network="${brName}" vlan=1
  lxc config device remove "${ctName}" ::1
  lxc config device add "${ctName}" _valid-name nic network="${brName}" vlan=1
  lxc config device remove "${ctName}" _valid-name
  lxc config device add "${ctName}" /foo nic network="${brName}" vlan=1
  lxc config device remove "${ctName}" /foo
  ! lxc config device add "${ctName}" .invalid nic network="${brName}" vlan=1 || false
  ! lxc config device add "${ctName}" ./invalid nic network="${brName}" vlan=1 || false
  ! lxc config device add "${ctName}" ../invalid nic network="${brName}" vlan=1 || false

  # Start instance.
  lxc start "${ctName}"

  # Check profile routes are applied on boot.
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep -F "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check profile limits are applied on boot.
  if ! tc class show dev "${vethHostName}" | grep -F "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep -F "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check that limits.priority was correctly configured in the firewall.
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -t mangle -S | grep -cF "${ctName} (${vethHostName}) netprio")" = "1" ]
    iptables -t mangle -S | grep -F "${ctName} (${vethHostName}) netprio" | grep -F "0000:0005"
  else
    [ "$(nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -cF "meta priority set")" = "1" ]
    nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -F "meta priority set 0:5"
  fi

  # Check profile custom MTU is applied in container on boot.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1400" ]; then
    echo "container veth mtu invalid"
    false
  fi

  # Check profile custom MTU doesn't affect the host.
  if [ "$(< /sys/class/net/"${vethHostName}"/mtu)" != "1500" ]; then
    echo "host veth mtu invalid"
    false
  fi

  # Check profile custom txqueuelen is applied in container on boot.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/tx_queue_len)" != "1200" ]; then
    echo "container veth txqueuelen invalid"
    false
  fi

  # Check profile custom txqueuelen is applied on host side of veth.
  if [ "$(< /sys/class/net/"${vethHostName}"/tx_queue_len)" != "1200" ]; then
    echo "host veth txqueuelen invalid"
    false
  fi

  # Check profile custom MAC is applied in container on boot.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/address)" != "${ctMAC}" ]; then
    echo "mac invalid"
    false
  fi

  # Add IP alias to container and check routes actually work.
  lxc exec "${ctName}" -- ip -6 addr add "2001:db8::1${ipRand}/128" dev eth0
  lxc exec "${ctName}" -- ip -4 addr add "192.0.2.1${ipRand}/32" dev eth0
  lxc exec "${ctName}" -- ip -4 route add default dev eth0
  ping -nc2 -i0.1 -W1 "192.0.2.1${ipRand}"
  wait_for_dad "${ctName}" eth0
  ping -6 -nc2 -i0.1 -W1 "2001:db8::1${ipRand}"

  # Test hot plugging a container nic with different settings to profile with the same name.
  lxc config device add "${ctName}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent="${brName}" \
    ipv4.routes="192.0.2.2${ipRand}/32" \
    ipv6.routes="2001:db8::2${ipRand}/128" \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    limits.priority=6 \
    host_name="${vethHostName}" \
    hwaddr="${ctMAC}" \
    mtu=1401

  # Test hot plugging a container nic with a different name.
  ! lxc config device add "${ctName}" eth1 nic nictype=bridged name=eth1 parent="${brName}" || false

  # Check profile routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep -F "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep -F "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check routes are applied on hot-plug.
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep -F "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check limits are applied on hot-plug.
  if ! tc class show dev "${vethHostName}" | grep -F "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep -F "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check that limits.priority was correctly configured in the firewall.
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -t mangle -S | grep -cF "${ctName} (${vethHostName}) netprio")" = "1" ]
    iptables -t mangle -S | grep -F "${ctName} (${vethHostName}) netprio" | grep -F "0000:0006"
  else
    [ "$(nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -cF "meta priority set")" = "1" ]
    nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -F "meta priority set 0:6"
  fi

  # Check custom MTU is applied on hot-plug.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1401" ]; then
    echo "container veth mtu invalid"
    false
  fi

  # Check custom MTU doesn't affect the host.
  if [ "$(< /sys/class/net/"${vethHostName}"/mtu)" != "1500" ]; then
    echo "host veth mtu invalid"
    false
  fi

  # Check custom MAC is applied on hot-plug.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/address)" != "${ctMAC}" ]; then
    echo "mac invalid"
    false
  fi

  # Test removing hot plugged device and check profile nic is restored.
  lxc config device remove "${ctName}" eth0

  # Check routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep -F "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep -F "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check profile routes are applied on hot-removal.
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep -F "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check profile limits are applie on hot-removal.
  if ! tc class show dev "${vethHostName}" | grep -F "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep -F "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check that limits.priority was correctly configured in the firewall.
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -t mangle -S | grep -cF "${ctName} (${vethHostName}) netprio")" = "1" ]
    iptables -t mangle -S | grep -F "${ctName} (${vethHostName}) netprio" | grep -F "0000:0005"
  else
    [ "$(nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -cF "meta priority set")" = "1" ]
    nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -F "meta priority set 0:5"
  fi

  # Check profile custom MTU is applied on hot-removal.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1400" ]; then
    echo "container veth mtu invalid"
    false
  fi

  # Check custom MTU doesn't affect the host.
  if [ "$(< /sys/class/net/"${vethHostName}"/mtu)" != "1500" ]; then
    echo "host veth mtu invalid"
    false
  fi

  # Test hot plugging a container nic then updating it.
  lxc config device add "${ctName}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent="${brName}" \
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
  lxc config device set "${ctName}" eth0 limits.priority 6
  lxc config device set "${ctName}" eth0 mtu 1402
  lxc config device set "${ctName}" eth0 hwaddr "${ctMAC}"

  # Check original routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep -F "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep -F "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep -F "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check limits are applied on update.
  if ! tc class show dev "${vethHostName}" | grep -F "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep -F "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check that limits.priority was correctly configured in the firewall.
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -t mangle -S | grep -cF "${ctName} (${vethHostName}) netprio")" = "1" ]
    iptables -t mangle -S | grep -F "${ctName} (${vethHostName}) netprio" | grep -F "0000:0006"
  else
    [ "$(nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -cF "meta priority set")" = "1" ]
    nft -nn list chain netdev lxd "egress.netprio.${ctName}.${vethHostName}" | grep -F "meta priority set 0:6"
  fi

  # Check custom MTU is applied update.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1402" ]; then
    echo "mtu invalid"
    false
  fi

  # Check custom MAC is applied update.
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/address)" != "${ctMAC}" ]; then
    echo "mac invalid"
    false
  fi

  # Check that MTU is inherited from parent device when not specified on device.
  lxc stop "${ctName}" --force
  lxc config device unset "${ctName}" eth0 mtu
  lxc network set "${brName}" bridge.mtu "1405"
  lxc start "${ctName}"
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1405" ]; then
    echo "mtu not inherited from parent"
    false
  fi
  lxc stop "${ctName}" --force
  lxc network unset "${brName}" bridge.mtu
  lxc start "${ctName}"

  # Add an external 3rd party route to the bridge interface and check that it and the container
  # routes remain when the network is reconfigured.
  ip -4 route add 192.0.2"${ipRand}".0/24 via 192.0.2.1"${ipRand}" dev "${brName}"

  # Now change something that will trigger a network restart
  lxc network set "${brName}" ipv4.nat false

  # Check external routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.2${ipRand}.0/24 via 192.0.2.1${ipRand}" ; then
    echo "external ipv4 routes invalid after network update"
    false
  fi

  # Check container routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.2.2${ipRand}" ; then
    echo "container ipv4 routes invalid after network update"
    false
  fi

  # Check network routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.3.0/24" ; then
    echo "network ipv4 routes invalid after network update"
    false
  fi

  # Check volatile cleanup on stop.
  lxc stop -f "${ctName}"
  if lxc config show "${ctName}" | grep -F volatile.eth0 ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Test DHCP lease clearance.
  lxc delete "${ctName}" -f
  lxc launch testimage "${ctName}" -d "${SMALL_ROOT_DISK}" -p "${ctName}"

  # Request DHCPv4 lease with custom name (to check managed name is allocated instead).
  lxc exec "${ctName}" -- udhcpc -f -i eth0 -n -q -t5 -F "${ctName}custom"

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
  if lxc exec "${ctName}" -- busybox --list | grep udhcpc6 ; then
        lxc exec "${ctName}" -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Check that dnsmasq will resolve on the lo
  # If testImage can't request a dhcp6 lease, it won't have an ip6 addr, so just
  # check the A record; we only care about access to dnsmasq here, not the
  # record itself.
  dig -4 +retry=0 +notcp @192.0.2.1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."
  dig -6 +retry=0 +notcp @2001:db8::1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."
  dig -4 +retry=0 +tcp @192.0.2.1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."
  dig -6 +retry=0 +tcp @2001:db8::1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."

  # Check that dnsmasq will resolve from the bridge
  # testImage doesn't have dig, so we create a netns with eth0 connected to the
  # bridge instead
  ip link add veth_left type veth peer veth_right
  ip link set veth_left master "${brName}" up

  ip netns add testdns
  ip link set dev veth_right netns testdns

  ip netns exec testdns ip link set veth_right name eth0
  ip netns exec testdns ip link set dev eth0 up
  ip netns exec testdns ip addr add 192.0.2.20/24 dev eth0
  ip netns exec testdns ip addr add 2001:db8::20/64 dev eth0 nodad

  ip addr
  ip netns exec testdns ip addr

  ip netns exec testdns dig -4 +retry=0 +notcp @192.0.2.1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."
  ip netns exec testdns dig -6 +retry=0 +notcp @2001:db8::1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."
  ip netns exec testdns dig -4 +retry=0 +tcp @192.0.2.1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."
  ip netns exec testdns dig -6 +retry=0 +tcp @2001:db8::1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2."

  ip netns exec testdns ip link delete eth0
  ip netns delete testdns

  # Ensure that dnsmasq is inaccessible from outside its managed bridge and the host lo
  # This creates a new net namespace `testdns`, a bridge `testbr0`, and veths
  # between; we need dns requests to come from an interface that isn't the
  # lxd-managed bridge or the host's loopback, and `dig` doesn't let you specify
  # the interface to use, only the source ip
  testbr0Addr4=10.10.10.1
  testbr0Addr6=fc00:feed:beef::1

  ip link add veth_left type veth peer veth_right
  ip link add testbr0 type bridge
  ip link set testbr0 up
  ip addr add "${testbr0Addr4}/24" dev testbr0
  ip addr add "${testbr0Addr6}/64" dev testbr0
  ip link set veth_left master testbr0 up

  ip netns add testdns
  ip link set dev veth_right netns testdns

  ip netns exec testdns ip link set veth_right name eth0
  ip netns exec testdns ip link set dev eth0 up
  ip netns exec testdns ip addr add 10.10.10.2/24 dev eth0
  ip netns exec testdns ip addr add fc00:feed:beef::2/64 dev eth0
  ip netns exec testdns ip route add default via "${testbr0Addr4}" dev eth0
  ip netns exec testdns ip -6 route add default via "${testbr0Addr6}" dev eth0

  ip addr
  ip netns exec testdns ip addr

  ! ip netns exec testdns dig -4 +retry=0 +notcp @192.0.2.1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2." || false
  ! ip netns exec testdns dig -6 +retry=0 +notcp @2001:db8::1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2." || false
  ! ip netns exec testdns dig -4 +retry=0 +tcp @192.0.2.1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2." || false
  ! ip netns exec testdns dig -6 +retry=0 +tcp @2001:db8::1 A "${ctName}.${dnsDomain}" | grep "${ctName}.${dnsDomain}.\\+0.\\+IN.\\+A.\\+192.0.2." || false

  ip netns exec testdns ip link delete eth0
  ip netns delete testdns
  ip link delete testbr0

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
  if [ -f "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not removed"
    false
  fi

  # Check dnsmasq host file is updated on new device.
  lxc init testimage "${ctName}" -d "${SMALL_ROOT_DISK}" -p "${ctName}"
  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0 ipv4.address=192.0.2.200 ipv6.address=2001:db8::200

  ls -lR "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/"

  if ! grep -F "192.0.2.200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config not updated with IPv4 address"
    false
  fi

  if ! grep -F "2001:db8::200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config not updated with IPv6 address"
    false
  fi

  lxc config device remove "${ctName}" eth0

  if grep -F "192.0.2.200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config still has old IPv4 address"
    false
  fi

  if grep -F "2001:db8::200" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config still has old IPv6 address"
    false
  fi

  # Check dnsmasq leases file removed if DHCP disabled and that device can be removed.
  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0
  lxc start "${ctName}"
  lxc exec "${ctName}" -- udhcpc -f -i eth0 -n -q -t5
  lxc network set "${brName}" ipv4.address none
  lxc network set "${brName}" ipv6.address none

  # Confirm IPv6 is disabled.
  [ "$(< "/proc/sys/net/ipv6/conf/${brName}/disable_ipv6")" = "1" ]

  if [ -f "${LXD_DIR}/networks/${brName}/dnsmasq.leases" ] ; then
    echo "dnsmasq.leases file still present after disabling DHCP"
    false
  fi

  if [ -f "${LXD_DIR}/networks/${brName}/dnsmasq.pid" ] ; then
    echo "dnsmasq.pid file still present after disabling DHCP"
    false
  fi

  lxc profile device unset "${ctName}" eth0 ipv6.routes
  lxc config device remove "${ctName}" eth0
  lxc stop -f "${ctName}"
  if [ -f "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not removed from network"
    false
  fi

  # Re-enable DHCP on network.
  lxc network set "${brName}" ipv4.address 192.0.2.1/24
  lxc network set "${brName}" ipv6.address 2001:db8::1/64
  lxc profile device set "${ctName}" eth0 ipv6.routes "2001:db8::1${ipRand}/128"

  # Confirm IPv6 is re-enabled.
  [ "$(< "/proc/sys/net/ipv6/conf/${brName}/disable_ipv6")" = "0" ]

  # Check dnsmasq host file is created on add.
  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0
  if [ ! -f "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not created"
    false
  fi

  # Check connecting device to non-managed bridged.
  ip link add "${ctName}" type dummy
  lxc config device set "${ctName}" eth0 parent "${ctName}"
  if [ -f "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not removed from old network"
    false
  fi

  ip link delete "${ctName}"

  # Linked network tests.
  # Can't use network property when parent is set.
  ! lxc profile device set "${ctName}" eth0 network="${brName}" || false

  # Remove mtu, nictype and parent settings and assign network in one command.
  lxc profile device set "${ctName}" eth0 mtu="" parent="" nictype="" network="${brName}"

  # Can't remove network if parent not specified.
  ! lxc profile device unset "${ctName}" eth0 network || false

  # Can't use some settings when network is set.
  ! lxc profile device set "${ctName}" eth0 nictype="bridged" || false
  ! lxc profile device set "${ctName}" eth0 mtu="1400" || false
  ! lxc profile device set "${ctName}" eth0 maas.subnet.ipv4="test" || false
  ! lxc profile device set "${ctName}" eth0 maas.subnet.ipv6="test" || false

  # Can't set static IP that isn't part of network's subnet.
  ! lxc profile device set "${ctName}" eth0 ipv4.address="192.0.4.2" || false
  ! lxc profile device set "${ctName}" eth0 ipv6.address="2001:db8:2::2" || false

  # Test bridge MTU is inherited.
  lxc network set "${brName}" bridge.mtu 1400
  lxc config device remove "${ctName}" eth0
  lxc start "${ctName}"
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1400" ]; then
    echo "container mtu has not been inherited from network"
    false
  fi

  # Check profile routes are applied on boot (as these use derived nictype).
  if ! ip -4 r list dev "${brName}" | grep -F "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep -F "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  lxc stop -f "${ctName}"
  lxc network unset "${brName}" bridge.mtu

  # Test stateful DHCP static IP checks.
  ! lxc config device override "${ctName}" eth0 ipv4.address="192.0.4.2" || false

  lxc network set "${brName}" ipv4.dhcp false
  ! lxc config device override "${ctName}" eth0 ipv4.address="192.0.2.2" || false
  lxc network unset "${brName}" ipv4.dhcp
  lxc config device override "${ctName}" eth0 ipv4.address="192.0.2.2"

  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8:2::2" || false

  lxc network set "${brName}" ipv6.dhcp=false ipv6.dhcp.stateful=false
  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2" || false
  lxc network set "${brName}" ipv6.dhcp=true ipv6.dhcp.stateful=false
  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2" || false
  lxc network set "${brName}" ipv6.dhcp=false ipv6.dhcp.stateful=true
  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2" || false

  lxc network unset "${brName}" ipv6.dhcp
  lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2"

  # Test port isolation.
  if bridge link set help 2>&1 | grep -wF isolated ; then
    lxc config device set "${ctName}" eth0 security.port_isolation true
    lxc start "${ctName}"
    bridge -d link show dev "${vethHostName}" | grep -F "isolated on"
    lxc stop -f "${ctName}"
  else
    echo "bridge command doesn't support port isolation, skipping port isolation checks"
  fi

  # Test interface naming scheme.
  lxc init testimage -d "${SMALL_ROOT_DISK}" test-naming
  lxc start test-naming
  lxc query "/1.0/instances/test-naming/state" | jq -r .network.eth0.host_name | grep ^veth
  lxc stop -f test-naming

  lxc config set instances.nic.host_name random
  lxc start test-naming
  lxc query "/1.0/instances/test-naming/state" | jq -r .network.eth0.host_name | grep ^veth
  lxc stop -f test-naming

  lxc config set instances.nic.host_name mac
  lxc start test-naming
  lxc query "/1.0/instances/test-naming/state" | jq -r .network.eth0.host_name | grep ^lxd
  lxc stop -f test-naming

  lxc config unset instances.nic.host_name
  lxc delete -f test-naming

  # Test new container with conflicting addresses can be created as a copy.
  lxc config device set "${ctName}" eth0 \
    ipv4.address=192.0.2.232 \
    hwaddr="" # Remove static MAC so that copies use new MAC (as changing MAC triggers device remove/add on snapshot restore).
  grep -F "192.0.2.232" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0"
  lxc copy "${ctName}" foo # Gets new MAC address but IPs still conflict.
  ! stat "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false
  lxc snapshot foo
  lxc export foo foo.tar.gz # Export in pristine state for import tests below.
  ! lxc start foo || false
  lxc config device set foo eth0 \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  grep -F "192.0.2.233" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  lxc start foo
  lxc stop -f foo

  # Test container snapshot with conflicting addresses can be restored.
  lxc restore foo snap0 # Test restore, IPs conflict on config device update (due to only IPs changing).
  ! stat "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false # Check lease file removed (due to non-user requested update failing).
  [ "$(lxc config device get foo eth0 ipv4.address)" = '192.0.2.232' ]
  ! lxc start foo || false
  lxc config device set foo eth0 \
    hwaddr="0a:92:a7:0d:b7:c9" \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  lxc start foo
  lxc stop -f foo

  lxc restore foo snap0 # Test restore, IPs conflict on config device remove/add (due to MAC change).
  ! stat "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false # Check lease file removed (due to MAC change).
  [ "$(lxc config device get foo eth0 ipv4.address)" = '192.0.2.232' ]
  ! lxc start foo || false
  lxc config device set foo eth0 \
    hwaddr="0a:92:a7:0d:b7:c9" \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  grep -F "192.0.2.233" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  lxc start foo
  lxc delete -f foo

  # Test container with conflicting addresses can be restored from backup.
  lxc import foo.tar.gz
  ! stat "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false
  ! lxc start foo || false
  [ "$(lxc config device get foo eth0 ipv4.address)" = '192.0.2.232' ]
  lxc config show foo/snap0 | grep -F 'ipv4.address: 192.0.2.232'
  lxc config device set foo eth0 \
    hwaddr="0a:92:a7:0d:b7:c9" \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  grep -F "192.0.2.233" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  [ "$(lxc config device get foo eth0 ipv4.address)" = '192.0.2.233' ]
  lxc start foo

  # Check MAC conflict detection:
  ! lxc config device set "${ctName}" eth0 hwaddr="0a:92:a7:0d:b7:c9" || false
  lxc delete -f foo

  # Test container can be imported with device override to fix conflict.
  lxc import foo.tar.gz --device eth0,ipv4.address=192.0.2.233 --device eth0,ipv6.address=2001:db8::3
  [ "$(lxc config device get foo eth0 ipv4.address)" = '192.0.2.233' ]
  [ "$(lxc config device get foo eth0 ipv6.address)" = '2001:db8::3' ]
  lxc start foo

  # Test container can be copied with device override to fix conflict.
  lxc copy foo foo-copy --device eth0,ipv4.address=192.0.2.234 --device eth0,ipv6.address=2001:db8::4 --device eth0,host_name=veth0 --device eth0,ipv4.routes=192.0.2.20/32 --device eth0,ipv6.routes=2001:db8::20/128
  [ "$(lxc config device get foo-copy eth0 ipv4.address)" = '192.0.2.234' ]
  [ "$(lxc config device get foo-copy eth0 ipv6.address)" = '2001:db8::4' ]
  [ "$(lxc config device get foo-copy eth0 ipv4.routes)" = '192.0.2.20/32' ]
  [ "$(lxc config device get foo-copy eth0 ipv6.routes)" = '2001:db8::20/128' ]
  lxc start foo-copy
  lxc delete -f foo-copy

  # Test snapshot can be copied with device override to fix conflict.
  lxc snapshot foo tester
  lxc copy foo/tester snap-copy --device eth0,ipv4.address=192.0.2.235 --device eth0,ipv6.address=2001:db8::5 --device eth0,host_name=veth1 --device eth0,ipv4.routes=192.0.2.21/32 --device eth0,ipv6.routes=2001:db8::21/128
  [ "$(lxc config device get snap-copy eth0 ipv4.address)" = '192.0.2.235' ]
  [ "$(lxc config device get snap-copy eth0 ipv6.address)" = '2001:db8::5' ]
  [ "$(lxc config device get snap-copy eth0 ipv4.routes)" = '192.0.2.21/32' ]
  [ "$(lxc config device get snap-copy eth0 ipv6.routes)" = '2001:db8::21/128' ]
  lxc start snap-copy
  lxc delete -f snap-copy
  lxc delete -f foo

  # Test container with conflicting addresses rebuilds DHCP lease if original conflicting instance is removed.
  ! stat "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false
  lxc import foo.tar.gz
  rm foo.tar.gz
  ! lxc start foo || false
  lxc delete "${ctName}" -f
  lxc start foo
  grep -F "192.0.2.232" "${LXD_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  lxc delete -f foo

  # Test container without extra network configuration can be restored from backup.
  lxc init testimage foo -d "${SMALL_ROOT_DISK}" -p "${ctName}"
  lxc export foo foo.tar.gz
  lxc import foo.tar.gz foo2
  rm foo.tar.gz
  lxc profile assign foo2 "${ctName}"
  lxc snapshot foo snap0

  # Test container start will fail due to volatile MAC conflict.
  [ "$(lxc config get foo volatile.eth0.hwaddr)" = "$(lxc config get foo2 volatile.eth0.hwaddr)" ]
  ! lxc start foo2 || false
  lxc delete -f foo2

  # Test snapshot can be copied to remote.
  lxc copy foo/snap0 localhost:foo3
  lxc delete -f foo foo3

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup.
  lxc profile delete "${ctName}"
  lxc network delete "${brName}"
}
