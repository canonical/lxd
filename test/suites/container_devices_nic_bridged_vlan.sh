test_container_devices_nic_bridged_vlan() {
  ensure_import_testimage
  prefix="lxdvlan$$"
  bridgeDriver=${LXD_NIC_BRIDGED_DRIVER:-"native"}

  if [ "$bridgeDriver" != "native" ] && [ "$bridgeDriver" != "openvswitch" ]; then
    echo "Unrecognised bridge driver: ${bridgeDriver}"
    false
  fi

  # Standard bridge with random subnet.
  lxc network create "${prefix}"
  lxc network set "${prefix}" bridge.driver "${bridgeDriver}"

  if [ "$bridgeDriver" = "native" ]; then
    if [ "$(< "/sys/class/net/${prefix}/bridge/vlan_filtering")" != "1" ]; then
      echo "vlan filtering not enabled on managed bridge interface"
      false
    fi

    if [ "$(< "/sys/class/net/${prefix}/bridge/default_pvid")" != "1" ]; then
      echo "vlan default PVID not 1 on managed bridge interface"
      false
    fi

    # Make sure VLAN filtering on bridge is disabled initially (for IP filtering tests).
    echo 0 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  # Create profile for new containers by atomically modifying nictype and parent to ensure validation passes.
  lxc profile show default | sed  "s/nictype: p2p/nictype: bridged\\n    parent: ${prefix}/" | lxc profile create "${prefix}"

  # Test tagged VLAN traffic is allowed when VLAN filtering and IP filtering are disabled.
  lxc launch testimage "${prefix}-ctA" -p "${prefix}"
  lxc launch testimage "${prefix}-ctB" -p "${prefix}"
  lxc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
  lxc exec "${prefix}-ctA" -- ip link set eth0.2 up
  lxc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
  lxc exec "${prefix}-ctB" -- ip link add link eth0 name eth0.2 type vlan id 2
  lxc exec "${prefix}-ctB" -- ip link set eth0.2 up
  lxc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0.2
  lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.2.2
  lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.2.1
  lxc stop -f "${prefix}-ctA"

  # Test tagged VLAN traffic is filtered when IP filtering is enabled.
  if [ "$bridgeDriver" = "native" ]; then
    lxc config device override "${prefix}-ctA" eth0 security.ipv4_filtering=true
    lxc start "${prefix}-ctA"
    lxc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
    lxc exec "${prefix}-ctA" -- ip link set eth0.2 up
    lxc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
    ! lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.2.2 || false
    ! lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.2.1 || false
    lxc stop -f "${prefix}-ctA"
    lxc config device remove "${prefix}-ctA" eth0
  fi

  # Test tagged VLAN traffic is filtered when using MAC filtering with spoofed MAC address.
  if [ "$bridgeDriver" = "native" ]; then
    lxc config device override "${prefix}-ctA" eth0 security.mac_filtering=true
    lxc start "${prefix}-ctA"
    lxc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
    lxc exec "${prefix}-ctA" -- ip link set eth0.2 up
    lxc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
    lxc exec "${prefix}-ctA" -- ip link set eth0.2 address 00:16:3e:92:f3:c1
    ! lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.2.2 || false
    ! lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.2.1 || false
    lxc stop -f "${prefix}-ctA"
    lxc config device remove "${prefix}-ctA" eth0
  fi

  # Test VLAN validation.
  lxc config device override "${prefix}-ctA" eth0 vlan=2 # Test valid untagged VLAN ID.
  lxc config device set "${prefix}-ctA" eth0 vlan.tagged="3, 4,5" # Test valid tagged VLAN ID list.
  lxc config device set "${prefix}-ctA" eth0 vlan.tagged="3,4-6" # Test valid tagged VLAN ID list with range.
  ! lxc config device set "${prefix}-ctA" eth0 vlan.tagged=3,2,4 || false # Test same tagged VLAN ID as untagged VLAN ID.
  ! lxc config device set "${prefix}-ctA" eth0 security.ipv4_filtering=true || false # Can't use IP filtering with VLANs.
  ! lxc config device set "${prefix}-ctA" eth0 security.ipv6_filtering=true || false # Can't use IP filtering with VLANs.
  ! lxc config device set "${prefix}-ctA" eth0 vlan=invalid || false # Check invalid VLAN ID.
  ! lxc config device set "${prefix}-ctA" eth0 vlan=4096 || false # Check out of range VLAN ID.
  ! lxc config device set "${prefix}-ctA" eth0 vlan=0 || false # Check out of range VLAN ID.
  ! lxc config device set "${prefix}-ctA" eth0 vlan.tagged="5,invalid, 6" || false # Check invalid VLAN ID list.
  ! lxc config device set "${prefix}-ctA" eth0 vlan.tagged=-1 || false # Check out of range VLAN ID list.
  ! lxc config device set "${prefix}-ctA" eth0 vlan.tagged=4096 || false # Check out of range VLAN ID list.
  ! lxc config device set "${prefix}-ctA" eth0 vlan.tagged=1,2,-3-4 || false # Check invalid VLAN ID range input
  ! lxc config device set "${prefix}-ctA" eth0 vlan.tagged=1,2,4-3 || false # Check invalid VLAN ID range boundary (declining range)
  lxc config device remove "${prefix}-ctA" eth0

  # Test untagged VLANs (and that tagged VLANs are filtered).
  if [ "$bridgeDriver" = "native" ]; then
    echo 1 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  lxc config device override "${prefix}-ctA" eth0 vlan=2
  lxc start "${prefix}-ctA"
  lxc exec "${prefix}-ctA" -- ip link set eth0 up
  lxc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0
  lxc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.3 type vlan id 3
  lxc exec "${prefix}-ctA" -- ip link set eth0.3 up
  lxc exec "${prefix}-ctA" -- ip a add 192.0.3.1/24 dev eth0.3
  lxc stop -f "${prefix}-ctB"
  lxc config device override "${prefix}-ctB" eth0 vlan=2
  lxc start "${prefix}-ctB"
  lxc exec "${prefix}-ctB" -- ip link set eth0 up
  lxc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0
  lxc exec "${prefix}-ctB" -- ip link add link eth0 name eth0.3 type vlan id 3
  lxc exec "${prefix}-ctB" -- ip link set eth0.3 up
  lxc exec "${prefix}-ctB" -- ip a add 192.0.3.2/24 dev eth0.3
  lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.2.2
  lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.2.1
  ! lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.3.2 || false
  ! lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.3.1 || false
  lxc stop -f "${prefix}-ctA"
  lxc config device remove "${prefix}-ctA" eth0
  lxc stop -f "${prefix}-ctB"
  lxc config device remove "${prefix}-ctB" eth0

  # Test tagged VLANs (and that vlan=none filters untagged frames).
  if [ "$bridgeDriver" = "native" ]; then
    echo 1 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  lxc config device override "${prefix}-ctA" eth0 vlan.tagged=2 vlan=none
  lxc start "${prefix}-ctA"
  lxc exec "${prefix}-ctA" -- ip link set eth0 up
  lxc exec "${prefix}-ctA" -- ip a add 192.0.3.1/24 dev eth0
  lxc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
  lxc exec "${prefix}-ctA" -- ip link set eth0.2 up
  lxc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
  lxc config device override "${prefix}-ctB" eth0 vlan.tagged=2 vlan=none
  lxc start "${prefix}-ctB"
  lxc exec "${prefix}-ctB" -- ip link set eth0 up
  lxc exec "${prefix}-ctB" -- ip a add 192.0.3.2/24 dev eth0
  lxc exec "${prefix}-ctB" -- ip link add link eth0 name eth0.2 type vlan id 2
  lxc exec "${prefix}-ctB" -- ip link set eth0.2 up
  lxc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0.2
  lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.2.2
  lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.2.1
  ! lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.3.2 || false
  ! lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.3.1 || false
  lxc stop -f "${prefix}-ctA"
  lxc config device remove "${prefix}-ctA" eth0
  lxc stop -f "${prefix}-ctB"
  lxc config device remove "${prefix}-ctB" eth0

  # Test custom default VLAN PVID is respected on unmanaged native bridge.
  if [ "$bridgeDriver" = "native" ]; then
    ip link add "${prefix}B" type bridge
    ip link set "${prefix}B" up
    echo 0 > "/sys/class/net/${prefix}B/bridge/vlan_filtering"
    echo 2 > "/sys/class/net/${prefix}B/bridge/default_pvid"
    lxc config device override "${prefix}-ctA" eth0 parent="${prefix}B" vlan.tagged=3
    ! lxc start "${prefix}-ctA" || false # Check it fails to start with vlan_filtering disabled.
    echo 1 > "/sys/class/net/${prefix}B/bridge/vlan_filtering"
    lxc start "${prefix}-ctA"
    lxc exec "${prefix}-ctA" -- ip link set eth0 up
    lxc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0
    lxc config device override "${prefix}-ctB" eth0 parent="${prefix}B" vlan=2 # Specify VLAN 2 explicitly (ctA is implicit).
    lxc start "${prefix}-ctB"
    lxc exec "${prefix}-ctB" -- ip link set eth0 up
    lxc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0
    lxc exec "${prefix}-ctA" -- ping -nc2 -i0.1 -W1 192.0.2.2
    lxc exec "${prefix}-ctB" -- ping -nc2 -i0.1 -W1 192.0.2.1
    lxc stop -f "${prefix}-ctA"
    lxc config device remove "${prefix}-ctA" eth0
    lxc stop -f "${prefix}-ctB"
    lxc config device remove "${prefix}-ctB" eth0
    ip link delete "${prefix}B"
  fi

  # Cleanup.
  lxc delete -f "${prefix}-ctA"
  lxc delete -f "${prefix}-ctB"
  lxc profile delete "${prefix}"
  lxc network delete "${prefix}"
}
