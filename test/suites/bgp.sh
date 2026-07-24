test_bgp() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  local BGP_ADDR="127.0.0.1"
  local BGP_PORT
  BGP_PORT="$(local_tcp_port)"
  local BGP_PORT_ALT
  BGP_PORT_ALT="$(local_tcp_port)"
  local BGP_ROUTER_ID="192.0.2.1"
  local BGP_ASN="65000"

  sub_test "Configure BGP listener and verify it is listening on the expected address and port"
  lxc config set core.bgp_address="${BGP_ADDR}:${BGP_PORT}" core.bgp_routerid="${BGP_ROUTER_ID}" core.bgp_asn="${BGP_ASN}"

  # Wait for the BGP listener to come up.
  for _ in $(seq 10); do
    nc -z "${BGP_ADDR}" "${BGP_PORT}" 2>/dev/null && break
    sleep 0.1
  done

  if ! nc -z "${BGP_ADDR}" "${BGP_PORT}" 2>/dev/null; then
    echo "ERROR: BGP listener did not come up on ${BGP_ADDR}:${BGP_PORT}, aborting" >&2
    exit 1
  fi

  sub_test "Advertise BGP prefixes and peers over the running listener"
  # Exercise the real gobgp AddPath/AddPeer code paths for both IPv4 and IPv6.
  # These were rewritten during the gobgp v3 -> v4 migration. With the listener
  # running, a prefix or peer is only recorded in the internal BGP state once
  # the underlying gobgp call has succeeded, so their presence confirms the
  # library calls still work.
  local BGP_NET="bgptest"
  lxc network create "${BGP_NET}" \
    ipv4.address="192.0.2.1/24" ipv4.nat=false \
    ipv6.address="2001:db8:bbbb::1/64" ipv6.nat=false \
    bgp.ipv4.nexthop="192.0.2.1" bgp.ipv6.nexthop="2001:db8:bbbb::1" \
    bgp.peers.test.address="127.0.0.2" bgp.peers.test.asn="65001" \
    bgp.peers.test.password="s3cret" bgp.peers.test.holdtime="30"

  # The listener must still be running after pushing the routes and peer.
  local output
  output="$(lxc query /internal/testing/bgp)"
  jq --exit-status '.server.running == true' <<< "${output}"

  # Both the IPv4 and IPv6 network subnets must be advertised.
  jq --exit-status '[.prefixes[].prefix] | any(. == "192.0.2.0/24")' <<< "${output}"
  jq --exit-status '[.prefixes[].prefix] | any(. == "2001:db8:bbbb::/64")' <<< "${output}"

  # The configured peer must be registered on the server.
  jq --exit-status '[.peers[].address] | any(. == "127.0.0.2")' <<< "${output}"

  sub_test "Remove a BGP peer and verify it is withdrawn"
  # Exercise the gobgp DeletePeer code path.
  lxc network set "${BGP_NET}" bgp.peers.test.address="" bgp.peers.test.asn="" bgp.peers.test.password="" bgp.peers.test.holdtime=""
  output="$(lxc query /internal/testing/bgp)"
  jq --exit-status '[.peers[].address] | any(. == "127.0.0.2") | not' <<< "${output}"

  sub_test "Delete the network and verify its BGP prefixes are withdrawn"
  # Exercise the gobgp DeletePath code path for both IPv4 and IPv6.
  lxc network delete "${BGP_NET}"
  output="$(lxc query /internal/testing/bgp)"
  jq --exit-status '[.prefixes[].prefix] | any(. == "192.0.2.0/24") | not' <<< "${output}"
  jq --exit-status '[.prefixes[].prefix] | any(. == "2001:db8:bbbb::/64") | not' <<< "${output}"

  sub_test "Reconfigure BGP listener to a different port and verify it follows"
  lxc config set core.bgp_address "${BGP_ADDR}:${BGP_PORT_ALT}"

  # Allow time for the old listener to stop and the new one to start.
  for _ in $(seq 10); do
    nc -z "${BGP_ADDR}" "${BGP_PORT_ALT}" 2>/dev/null && break
    sleep 0.1
  done

  if ! nc -z "${BGP_ADDR}" "${BGP_PORT_ALT}" 2>/dev/null; then
    echo "ERROR: BGP listener did not come up on ${BGP_ADDR}:${BGP_PORT_ALT} after reconfiguration, aborting" >&2
    exit 1
  fi

  # Wait for the old listener to stop (shutdown is asynchronous).
  for _ in $(seq 10); do
    nc -z "${BGP_ADDR}" "${BGP_PORT}" 2>/dev/null || break
    sleep 0.1
  done

  # The old port must no longer be in use.
  if nc -z "${BGP_ADDR}" "${BGP_PORT}" 2>/dev/null; then
    echo "ERROR: BGP listener is still up on old port ${BGP_PORT} after reconfiguration, aborting" >&2
    exit 1
  fi

  sub_test "Unconfigure BGP listener and verify it is no longer listening"
  lxc config set core.bgp_address="" core.bgp_routerid="" core.bgp_asn=""

  for _ in $(seq 10); do
    nc -z "${BGP_ADDR}" "${BGP_PORT_ALT}" 2>/dev/null || break
    sleep 0.1
  done

  if nc -z "${BGP_ADDR}" "${BGP_PORT_ALT}" 2>/dev/null; then
    echo "ERROR: BGP listener is still up on ${BGP_ADDR}:${BGP_PORT_ALT} after unconfiguration, aborting" >&2
    exit 1
  fi
}
