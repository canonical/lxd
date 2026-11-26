test_container_devices_nic_p2p_peer_vm() {
  ensure_import_ubuntu_vm_image

  vm1="p2p-vm1$$"
  vm2="p2p-vm2$$"
  mac1="00:16:3e:aa:bb:01"
  mac2="00:16:3e:aa:bb:02"
  ipRand=$(shuf -i 1-254 -n 1)
  subnet1="198.18.${ipRand}"
  subnet2="198.19.${ipRand}"

  p2p_peer_vm_iface() {
    local vm="$1"
    local mac="$2"

    lxc exec "${vm}" -- /bin/ip -o link | awk -v mac="${mac}" 'tolower($0) ~ tolower(mac) {print $2}' | cut -d: -f1 | head -n1 || true
  }

  p2p_peer_vm_find_ifaces() {
    for _ in $(seq 1 60); do
      iface1=$(p2p_peer_vm_iface "${vm1}" "${mac1}")
      iface2=$(p2p_peer_vm_iface "${vm2}" "${mac2}")
      if [ -n "${iface1}" ] && [ -n "${iface2}" ]; then
        return 0
      fi

      sleep 1
    done

    return 1
  }

  p2p_peer_vm_connect() {
    local vm="$1"
    local addr="$2"
    local output

    # A successful TCP connect, or a "Connection refused" from the peer, proves
    # that the SYN traversed the private link and reached the peer's IP stack.
    # No guest service or background process is required.
    for _ in $(seq 1 10); do
      if output=$(lxc exec "${vm}" -- /usr/bin/timeout 5 /bin/bash -c "exec 3<>/dev/tcp/${addr}/22" 2>&1); then
        return 0
      fi

      if grep -q "Connection refused" <<< "${output}"; then
        return 0
      fi

      sleep 1
    done

    echo "failed to reach ${addr}:22 from ${vm}: ${output}"
    return 1
  }

  test_container_devices_nic_p2p_peer_vm_cleanup() {
    lxc stop -f "${vm1}" "${vm2}" 2>/dev/null || true
    lxc config device set "${vm1}" eth1 peer "" 2>/dev/null || true
    lxc config device set "${vm2}" eth1 peer "" 2>/dev/null || true
    lxc delete -f "${vm1}" "${vm2}" 2>/dev/null || true
  }

  # Keep the harness's EXIT trap (which stops the temporary LXD daemon) and run our
  # cleanup before it. Do not name the function "cleanup" as that would shadow it.
  local orig_exit_trap
  orig_exit_trap=$(trap -p EXIT | sed -E "s/^trap -- '(.*)' EXIT$/\1/")
  # shellcheck disable=SC2064 # Expand the captured harness trap now, not when signalled.
  trap "test_container_devices_nic_p2p_peer_vm_cleanup; ${orig_exit_trap}" EXIT

  startNicCount=$(find /sys/class/net | wc -l)

  lxc init ubuntu-vm "${vm1}" --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"
  lxc init ubuntu-vm "${vm2}" --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"

  # Configure both sides of the pair. The MAC is used to find the interface inside
  # the guest, where predictable network naming may not use the configured name.
  lxc config device add "${vm1}" eth1 nic nictype=p2p name=eth1 hwaddr="${mac1}" peer="${vm2}:eth1"
  lxc config device add "${vm2}" eth1 nic nictype=p2p name=eth1 hwaddr="${mac2}" peer="${vm1}:eth1"

  # Start in reverse order to prove that start order does not matter.
  lxc start "${vm2}"
  waitInstanceReady "${vm2}"
  lxc start "${vm1}"
  waitInstanceReady "${vm1}"

  sleep 1
  if ! p2p_peer_vm_find_ifaces; then
    echo "peer NICs not found in the VMs"
    false
  fi

  echo "peer interfaces: ${iface1} ${iface2}"

  lxc exec "${vm1}" -- /bin/ip link set "${iface1}" up
  lxc exec "${vm2}" -- /bin/ip link set "${iface2}" up
  lxc exec "${vm1}" -- /bin/ip addr add "${subnet1}.1/24" dev "${iface1}"
  lxc exec "${vm2}" -- /bin/ip addr add "${subnet1}.2/24" dev "${iface2}"
  p2p_peer_vm_connect "${vm1}" "${subnet1}.2"

  # Check that tagged VLANs traverse the same link.
  lxc exec "${vm1}" -- /bin/ip link add link "${iface1}" name "${iface1}.100" type vlan id 100
  lxc exec "${vm2}" -- /bin/ip link add link "${iface2}" name "${iface2}.100" type vlan id 100
  lxc exec "${vm1}" -- /bin/ip link set "${iface1}.100" up
  lxc exec "${vm2}" -- /bin/ip link set "${iface2}.100" up
  lxc exec "${vm1}" -- /bin/ip addr add "${subnet2}.1/24" dev "${iface1}.100"
  lxc exec "${vm2}" -- /bin/ip addr add "${subnet2}.2/24" dev "${iface2}.100"
  p2p_peer_vm_connect "${vm1}" "${subnet2}.2"

  # The private bridge must be up, have no addresses and be unreachable from the host.
  bridgeName=$(p2pPeerBridgeName "${vm1}" eth1)
  [ -n "${bridgeName}" ]
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

  if ping -nc1 -W1 "${subnet1}.1" >/dev/null 2>&1 ; then
    echo "host reached a peered VM through the private bridge"
    false
  fi

  p2pPeerBridgeSecure "${bridgeName}"

  # Restart one side and check the other side keeps its interface and the link recovers.
  # Use separate stop/start so no single lxc command can exceed the harness's 120s limit.
  lxc stop -f "${vm1}"
  lxc start "${vm1}"
  waitInstanceReady "${vm1}"
  sleep 1
  iface1=$(p2p_peer_vm_iface "${vm1}" "${mac1}")
  [ -n "${iface1}" ]
  lxc exec "${vm2}" -- /usr/bin/test -e "/sys/class/net/${iface2}"
  lxc exec "${vm1}" -- /bin/ip link set "${iface1}" up
  lxc exec "${vm1}" -- /bin/ip addr add "${subnet1}.1/24" dev "${iface1}"
  p2p_peer_vm_connect "${vm1}" "${subnet1}.2"

  # Stop one side and check the other side keeps its interface.
  lxc stop -f "${vm2}"
  lxc exec "${vm1}" -- /usr/bin/test -e "/sys/class/net/${iface1}"
  lxc start "${vm2}"
  waitInstanceReady "${vm2}"
  sleep 1
  iface2=$(p2p_peer_vm_iface "${vm2}" "${mac2}")
  [ -n "${iface2}" ]
  lxc exec "${vm2}" -- /bin/ip link set "${iface2}" up
  lxc exec "${vm2}" -- /bin/ip addr add "${subnet1}.2/24" dev "${iface2}"
  p2p_peer_vm_connect "${vm1}" "${subnet1}.2"

  # The peer option cannot be changed while either instance is running.
  if lxc config device set "${vm1}" eth1 peer "" ; then
    echo "peer was changed while the instance was running"
    false
  fi

  test_container_devices_nic_p2p_peer_vm_cleanup
  # shellcheck disable=SC2064 # Restore the captured harness trap.
  trap "${orig_exit_trap}" EXIT

  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi
}
