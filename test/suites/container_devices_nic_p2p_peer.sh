test_container_devices_nic_p2p_peer() {
  ensure_import_testimage

  ct1="p2p-peer1$$"
  ct2="p2p-peer2$$"
  proj=""
  rct1=""
  rct2=""
  ipRand=$(shuf -i 1-254 -n 1)
  subnet1="198.18.${ipRand}"
  subnet2="198.19.${ipRand}"
  subnet3="198.20.${ipRand}"

  test_container_devices_nic_p2p_peer_cleanup() {
    lxc stop -f "${ct1}" "${ct2}" 2>/dev/null || true
    lxc config device set "${ct1}" eth1 peer "" 2>/dev/null || true
    lxc config device set "${ct2}" eth1 peer "" 2>/dev/null || true
    lxc config device set "${ct1}" eth2 peer "" 2>/dev/null || true
    lxc config device set "${ct2}" eth2 peer "" 2>/dev/null || true
    lxc delete -f "${ct1}" "${ct2}" 2>/dev/null || true

    if [ -n "${proj}" ]; then
      lxc --project "${proj}" delete -f "${rct1}" "${rct2}" 2>/dev/null || true
      lxc project delete "${proj}" 2>/dev/null || true
    fi
  }

  # Keep the harness's EXIT trap (which stops the temporary LXD daemon) and run our
  # cleanup before it. Do not name the function "cleanup" as that would shadow it.
  local orig_exit_trap
  orig_exit_trap=$(trap -p EXIT | sed -E "s/^trap -- '(.*)' EXIT$/\1/")
  # shellcheck disable=SC2064 # Expand the captured harness trap now, not when signalled.
  trap "test_container_devices_nic_p2p_peer_cleanup; ${orig_exit_trap}" EXIT

  startNicCount=$(find /sys/class/net | wc -l)

  lxc init testimage "${ct1}" -c boot.autostart=false
  lxc init testimage "${ct2}" -c boot.autostart=false

  # Configure two independent links between the same instances.
  lxc config device add "${ct1}" eth1 nic nictype=p2p name=eth1 peer="${ct2}:eth1"
  lxc config device add "${ct2}" eth1 nic nictype=p2p name=eth1 peer="${ct1}:eth1"
  lxc config device add "${ct1}" eth2 nic nictype=p2p name=eth2 peer="${ct2}:eth2"
  lxc config device add "${ct2}" eth2 nic nictype=p2p name=eth2 peer="${ct1}:eth2"

  # Start in reverse order to prove that start order does not matter.
  lxc start "${ct2}"
  lxc start "${ct1}"

  for _ in $(seq 1 10); do
    if lxc exec "${ct1}" -- test -e /sys/class/net/eth1 && lxc exec "${ct2}" -- test -e /sys/class/net/eth1 && lxc exec "${ct1}" -- test -e /sys/class/net/eth2 && lxc exec "${ct2}" -- test -e /sys/class/net/eth2; then
      break
    fi

    sleep 1
  done

  lxc exec "${ct1}" -- ip addr add "${subnet1}.1/24" dev eth1
  lxc exec "${ct2}" -- ip addr add "${subnet1}.2/24" dev eth1
  lxc exec "${ct1}" -- ping -nc2 -W1 "${subnet1}.2"

  lxc exec "${ct1}" -- ip addr add "${subnet3}.1/24" dev eth2
  lxc exec "${ct2}" -- ip addr add "${subnet3}.2/24" dev eth2
  lxc exec "${ct1}" -- ping -nc2 -W1 "${subnet3}.2"

  # Check that tagged VLANs traverse the same link.
  lxc exec "${ct1}" -- ip link add link eth1 name eth1.100 type vlan id 100
  lxc exec "${ct2}" -- ip link add link eth1 name eth1.100 type vlan id 100
  lxc exec "${ct1}" -- ip link set eth1.100 up
  lxc exec "${ct2}" -- ip link set eth1.100 up
  lxc exec "${ct1}" -- ip addr add "${subnet2}.1/24" dev eth1.100
  lxc exec "${ct2}" -- ip addr add "${subnet2}.2/24" dev eth1.100
  lxc exec "${ct1}" -- ping -nc2 -W1 "${subnet2}.2"

  # The private bridges must be distinct, up, have no addresses and be unreachable
  # from the host.
  bridgeName1=$(p2pPeerBridgeName "${ct1}" eth1)
  bridgeName2=$(p2pPeerBridgeName "${ct1}" eth2)
  [ -n "${bridgeName1}" ]
  [ -n "${bridgeName2}" ]
  [ "${bridgeName1}" != "${bridgeName2}" ]

  for bridgeName in "${bridgeName1}" "${bridgeName2}"; do
    [ "$(cat "/sys/class/net/${bridgeName}/operstate")" = "up" ]

    bridgeAddr4=$(ip -4 addr show dev "${bridgeName}")
    if grep -q "inet " <<< "${bridgeAddr4}"; then
      echo "private bridge has an IPv4 address"
      false
    fi

    bridgeAddr6=$(ip -6 addr show dev "${bridgeName}")
    if grep -q "inet6 " <<< "${bridgeAddr6}"; then
      echo "private bridge has an IPv6 address"
      false
    fi

    p2pPeerBridgeSecure "${bridgeName}"
  done

  if ping -nc1 -W1 "${subnet1}.1" >/dev/null 2>&1 ; then
    echo "host reached a peered instance through the private bridge"
    false
  fi

  # Restart one side and check the other side keeps its interface and the link recovers.
  lxc restart "${ct1}"
  lxc exec "${ct2}" -- test -e /sys/class/net/eth1
  lxc exec "${ct2}" -- test -e /sys/class/net/eth2
  lxc exec "${ct1}" -- ip addr add "${subnet1}.1/24" dev eth1
  lxc exec "${ct1}" -- ip addr add "${subnet3}.1/24" dev eth2
  lxc exec "${ct1}" -- ping -nc2 -W1 "${subnet1}.2"
  lxc exec "${ct1}" -- ping -nc2 -W1 "${subnet3}.2"

  # Stop one side and check the other side keeps its interface.
  lxc stop "${ct2}"
  lxc exec "${ct1}" -- test -e /sys/class/net/eth1
  lxc exec "${ct1}" -- test -e /sys/class/net/eth2
  p2pPeerBridgeSecure "${bridgeName1}"
  p2pPeerBridgeSecure "${bridgeName2}"
  lxc start "${ct2}"
  lxc exec "${ct2}" -- ip addr add "${subnet1}.2/24" dev eth1
  lxc exec "${ct2}" -- ip addr add "${subnet3}.2/24" dev eth2
  lxc exec "${ct1}" -- ping -nc2 -W1 "${subnet1}.2"
  lxc exec "${ct1}" -- ping -nc2 -W1 "${subnet3}.2"

  sub_test "Full daemon shutdown removes empty peer bridges and filters"
  local firewall rules
  firewall=$(lxc info | awk '/firewall:/{print $2}')
  lxd shutdown --timeout 90
  case "${firewall}" in
    nftables) rules=$(nft list ruleset) ;;
    xtables) rules=$(ebtables -L INPUT --Lx) ;;
    *) echo "Unknown firewall driver: ${firewall}"; return 1 ;;
  esac
  for bridgeName in "${bridgeName1}" "${bridgeName2}"; do
    [ ! -e "/sys/class/net/${bridgeName}" ]
    if grep -Fq "${bridgeName}" <<< "${rules}"; then
      echo "Filter for stopped peer bridge ${bridgeName} still exists"
      return 1
    fi
  done

  respawn_lxd "${LXD_DIR}" true
  lxc start "${ct2}"
  p2pPeerBridgeSecure "${bridgeName1}"
  p2pPeerBridgeSecure "${bridgeName2}"
  lxc start "${ct1}"
  lxc exec -T -n "${ct1}" -- ip addr add "${subnet1}.1/24" dev eth1
  lxc exec -T -n "${ct2}" -- ip addr add "${subnet1}.2/24" dev eth1
  lxc exec -T -n "${ct1}" -- ping -nc2 -W1 "${subnet1}.2"

  # The peer option cannot be changed while either instance is running.
  if lxc config device set "${ct1}" eth1 peer "" ; then
    echo "peer was changed while the instance was running"
    false
  fi

  # Replacing a peered NIC while the instance is running must be refused; otherwise the
  # removal half would delete the shared bridge and the peer's port.
  if lxc config device set "${ct1}" eth1 hwaddr="00:16:3e:aa:bb:cc" ; then
    echo "peered NIC was replaced while the instance was running"
    false
  fi

  lxc exec "${ct1}" -- test -e /sys/class/net/eth1
  lxc exec "${ct2}" -- test -e /sys/class/net/eth1

  # Invalid peer configurations are rejected.
  if lxc config device add "${ct1}" bad nic nictype=p2p peer="${ct2}:eth1" mtu=1400 ; then
    echo "peer accepted mtu"
    false
  fi

  if lxc config device add "${ct1}" bad nic nictype=p2p peer="${ct2}:eth1" queue.tx.length=1000 ; then
    echo "peer accepted queue.tx.length"
    false
  fi

  if lxc config device add "${ct1}" bad nic nictype=p2p peer="${ct1}:eth1" ; then
    echo "self peer accepted"
    false
  fi

  if lxc config device add "${ct1}" eth3 nic nictype=p2p peer="${ct2}:eth1" ; then
    echo "peer device already paired accepted"
    false
  fi

  lxc profile create p2p-peer-invalid 2>/dev/null || true
  if lxc profile device add p2p-peer-invalid eth9 nic nictype=p2p peer="${ct1}:eth1" ; then
    echo "profile peer accepted"
    false
  fi

  lxc profile delete p2p-peer-invalid 2>/dev/null || true

  # Peer links work in a restricted project with the default managed NIC policy,
  # and the project cannot tamper with the volatile link state to reach a host
  # bridge that it does not own.
  proj="p2p-peer-restricted$$"
  rct1="p2p-peer-r1$$"
  rct2="p2p-peer-r2$$"
  pool=$(lxc profile device get default root pool)
  lxc project create "${proj}" -c features.images=false -c restricted=true -c restricted.devices.nic=managed
  lxc --project "${proj}" profile device add default root disk path="/" pool="${pool}"
  sub_test "Hotplug a peer link under the managed NIC policy"
  lxc --project "${proj}" launch testimage "${rct1}"
  lxc --project "${proj}" launch testimage "${rct2}"
  lxc --project "${proj}" config device add "${rct1}" eth1 nic nictype=p2p name=eth1 peer="${rct2}:eth1"

  local waitingPort waitingBridge
  waitingPort=$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.host_name)
  waitingBridge=$(basename "$(readlink -e "/sys/class/net/${waitingPort}/master")")
  p2pPeerBridgeSecure "${waitingBridge}"
  [ "$(ls "/sys/class/net/${waitingBridge}/brif")" = "${waitingPort}" ]
  [ -z "$(lxc --project "${proj}" config get "${rct2}" volatile.eth1.peer.link_id || echo fail)" ]
  lxc --project "${proj}" exec -T -n "${rct1}" -- test -e /sys/class/net/eth1

  # A peered p2p NIC is allowed, but other unmanaged NICs are still refused.
  if lxc --project "${proj}" config device add "${rct1}" eth9 nic nictype=p2p ; then
    echo "unmanaged p2p NIC accepted in a managed restricted project"
    false
  fi

  lxc --project "${proj}" config device add "${rct2}" eth1 nic nictype=p2p name=eth1 peer="${rct1}:eth1"

  linkID=$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.peer.link_id)
  hostName=$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.host_name)
  [ -n "${linkID}" ]
  [ -n "${hostName}" ]
  [ -e "/sys/class/net/${hostName}" ]

  # Neither the link ID nor the port name can be changed from a restricted project.
  if lxc --project "${proj}" config set "${rct1}" volatile.eth1.peer.link_id="8f0e6b1a-0000-4000-8000-000000000000" ; then
    echo "restricted project changed a peer link ID"
    false
  fi

  if lxc --project "${proj}" config set "${rct1}" volatile.eth1.host_name="br0" ; then
    echo "restricted project changed a peer host name"
    false
  fi

  if lxc --project "${proj}" config unset "${rct1}" volatile.eth1.peer.link_id ; then
    echo "restricted project removed a peer link ID"
    false
  fi

  if lxc --project "${proj}" config unset "${rct1}" volatile.eth1.host_name ; then
    echo "restricted project removed a peer host name"
    false
  fi

  [ "$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.peer.link_id)" = "${linkID}" ]
  [ "$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.host_name)" = "${hostName}" ]

  # The test image does not configure interfaces added after boot.
  lxc --project "${proj}" exec -T -n "${rct1}" -- ip link set dev eth1 up
  lxc --project "${proj}" exec -T -n "${rct2}" -- ip link set dev eth1 up
  lxc --project "${proj}" exec "${rct1}" -- ip addr add "${subnet3}.3/24" dev eth1
  lxc --project "${proj}" exec "${rct2}" -- ip addr add "${subnet3}.4/24" dev eth1
  lxc --project "${proj}" exec "${rct1}" -- ping -nc2 -W1 "${subnet3}.4"

  # The link ID is stable across a restart, and the port is recreated with a new name.
  lxc --project "${proj}" restart "${rct1}"
  [ "$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.peer.link_id)" = "${linkID}" ]
  hostName=$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.host_name)
  [ -n "${hostName}" ]
  [ -e "/sys/class/net/${hostName}" ]
  lxc --project "${proj}" exec "${rct1}" -- ip addr add "${subnet3}.3/24" dev eth1
  lxc --project "${proj}" exec "${rct1}" -- ping -nc2 -W1 "${subnet3}.4"

  sub_test "Hot-remove and reconnect while the other endpoint stays up"
  lxc --project "${proj}" config device remove "${rct1}" eth1
  lxc --project "${proj}" exec -T -n "${rct1}" -- test ! -e /sys/class/net/eth1
  lxc --project "${proj}" exec -T -n "${rct2}" -- test -e /sys/class/net/eth1
  lxc --project "${proj}" config device add "${rct1}" eth1 nic nictype=p2p name=eth1 peer="${rct2}:eth1"
  [ "$(lxc --project "${proj}" config get "${rct1}" volatile.eth1.peer.link_id)" = "${linkID}" ]
  lxc --project "${proj}" exec -T -n "${rct1}" -- ip link set dev eth1 up
  lxc --project "${proj}" exec -T -n "${rct1}" -- ip addr add "${subnet3}.3/24" dev eth1
  lxc --project "${proj}" exec -T -n "${rct1}" -- ping -nc2 -W1 "${subnet3}.4"
  lxc --project "${proj}" config device remove "${rct1}" eth1
  lxc --project "${proj}" config device remove "${rct2}" eth1
  [ ! -e "/sys/class/net/${waitingBridge}" ]

  # Deleting one instance is allowed while its peer still references it; the
  # surviving instance keeps a dangling peer reference and its link keeps working.
  lxc delete -f "${ct1}"
  [ "$(lxc config device get "${ct2}" eth1 peer)" = "${ct1}:eth1" ]
  [ "$(lxc config device get "${ct2}" eth2 peer)" = "${ct1}:eth2" ]
  lxc exec "${ct2}" -- test -e /sys/class/net/eth1
  lxc exec "${ct2}" -- test -e /sys/class/net/eth2
  [ -n "$(p2pPeerBridgeName "${ct2}" eth1)" ]
  [ -n "$(p2pPeerBridgeName "${ct2}" eth2)" ]

  lxc stop "${ct2}"

  sub_test "Relink a recreated endpoint in either start order"
  local retainedID first second
  retainedID=$(lxc config get "${ct2}" volatile.eth1.peer.link_id)
  [ -n "${retainedID}" ]
  for first in "${ct2}" "${ct1}"; do
    lxc init testimage "${ct1}"
    lxc config device add "${ct1}" eth1 nic nictype=p2p name=eth1 peer="${ct2}:eth1"
    lxc config device add "${ct1}" eth2 nic nictype=p2p name=eth2 peer="${ct2}:eth2"
    second="${ct2}"
    if [ "${first}" = "${ct2}" ]; then
      second="${ct1}"
    fi

    lxc start "${first}"
    lxc start "${second}"
    [ "$(lxc config get "${ct1}" volatile.eth1.peer.link_id)" = "${retainedID}" ]
    [ "$(lxc config get "${ct2}" volatile.eth1.peer.link_id)" = "${retainedID}" ]
    lxc exec -T -n "${ct1}" -- ip addr add "${subnet1}.1/24" dev eth1
    lxc exec -T -n "${ct2}" -- ip addr add "${subnet1}.2/24" dev eth1
    lxc exec -T -n "${ct1}" -- ping -nc2 -W1 "${subnet1}.2"
    lxc delete -f "${ct1}"
    lxc stop "${ct2}"
  done

  # Unsetting the peer option and stopping the device removes the leftover link state.
  lxc config device set "${ct2}" eth1 peer ""
  lxc config device set "${ct2}" eth2 peer ""
  lxc start "${ct2}"
  lxc stop "${ct2}"
  [ -z "$(lxc config get "${ct2}" volatile.eth1.peer.link_id || echo fail)" ]
  [ -z "$(lxc config get "${ct2}" volatile.eth2.peer.link_id || echo fail)" ]

  test_container_devices_nic_p2p_peer_cleanup
  # shellcheck disable=SC2064 # Restore the captured harness trap.
  trap "${orig_exit_trap}" EXIT

  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi
}
