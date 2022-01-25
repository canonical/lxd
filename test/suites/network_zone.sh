test_network_zone() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Enable the DNS server
  lxc config unset core.https_address
  lxc config set core.dns_address "${LXD_ADDR}"

  # Create a network
  netName=lxdt$$
  lxc network create "${netName}" \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=fd42:4242:4242:1010::1/64

  # Create the zones
  lxc network zone create lxd.example.net
  lxc network zone create 2.0.192.in-addr.arpa
  lxc network zone create 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa

  # Link the zones to the network
  lxc network set "${netName}" \
    dns.zone.forward=lxd.example.net \
    dns.zone.reverse.ipv4=2.0.192.in-addr.arpa \
    dns.zone.reverse.ipv6=0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa

  # Put an instance on the network
  lxc init testimage c1 --network "${netName}"
  lxc config device set c1 eth0 ipv4.address=192.0.2.42
  lxc start c1

  # Wait for IPv4 and IPv6 addresses
  while :; do
    sleep 1
    [ -n "$(lxc list -c6 --format=csv c1)" ] || continue
    break
  done

  # Setup DNS peers
  lxc network zone set lxd.example.net peers.test.address=127.0.0.1
  lxc network zone set 2.0.192.in-addr.arpa peers.test.address=127.0.0.1
  lxc network zone set 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa peers.test.address=127.0.0.1

  # Check the zones
  DNS_ADDR="$(echo "${LXD_ADDR}" | cut -d: -f1)"
  DNS_PORT="$(echo "${LXD_ADDR}" | cut -d: -f2)"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep -v "SOA" | grep "A"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep "AAAA"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep "PTR"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep "PTR"

  # Test extra records
  lxc network zone record create lxd.example.net demo user.foo=bar
  lxc network zone record entry add lxd.example.net demo A 1.1.1.1 --ttl 900
  lxc network zone record entry add lxd.example.net demo A 2.2.2.2
  lxc network zone record entry add lxd.example.net demo AAAA 1111::1111 --ttl 1800
  lxc network zone record entry add lxd.example.net demo AAAA 2222::2222
  lxc network zone record entry add lxd.example.net demo MX "1 mx1.example.net." --ttl 900
  lxc network zone record entry add lxd.example.net demo MX "10 mx2.example.net." --ttl 900
  lxc network zone record list lxd.example.net
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep demo

  # Cleanup
  lxc delete -f c1
  lxc network delete "${netName}"
  lxc network zone delete lxd.example.net
  lxc network zone delete 2.0.192.in-addr.arpa
  lxc network zone delete 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa

  lxc config unset core.dns_address
  lxc config set core.https_address "${LXD_ADDR}"
}
