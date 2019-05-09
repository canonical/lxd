test_container_devices_nic_physical() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  ct_name="nt$$"

  # Create dummy interface for use as parent.
  ip link add "${ct_name}" type dummy
  ip link set "${ct_name}" up

  # Create test container from default profile.
  lxc init testimage "${ct_name}"

  # Add physical device to container/
  lxc config device add "${ct_name}" eth0 nic \
    nictype=physical \
    parent="${ct_name}" \
    name=eth0 \
    mtu=1400

  # Lauch container and check it has nic applied correctly.
  lxc start "${ct_name}"

  # Check custom MTU is applied if feature available in LXD.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! lxc exec "${ct_name}" -- ip link show eth0 | grep "mtu 1400" ; then
      echo "mtu invalid"
      false
    fi
  fi

  lxc config device remove "${ct_name}" eth0

  # Test hot-plugging physical device based on vlan parent.
  ip link set "${ct_name}" up
  lxc config device add "${ct_name}" eth0 nic \
    nictype=physical \
    parent="${ct_name}" \
    name=eth0 \
    vlan=10 \
    mtu=1399 #This must be less than or equal to the MTU of the parent device (which is not being reset)

  # Check custom MTU is applied.
  if ! lxc exec "${ct_name}" -- ip link show eth0 | grep "mtu 1399" ; then
    echo "mtu invalid"
    false
  fi

  # Destroy the container and check the physical interface is returned to the host for clean up.
  lxc delete "${ct_name}" -f

  # Remove dummy interface if present.
  ip link delete "${ct_name}" || true
}
