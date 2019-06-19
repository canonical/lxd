# Activating SR-IOV VFs:
# Mellanox:
# sudo rmmod mlx4_ib mlx4_en mlx4_core
# sudo modprobe mlx4_core num_vfs=2,0 probe_vf=2,0
#
# Intel:
# sudo rmmod igb
# sudo modprobe igb max_vfs=2
test_container_devices_nic_sriov() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  parent=${LXD_NIC_SRIOV_PARENT:-""}

  if [ "$parent" = "" ]; then
    echo "==> SKIP: No SR-IOV NIC parent specified"
    return
  fi

  ctName="nt$$"
  macRand=$(shuf -i 0-9 -n 1)
  ctMAC1="da:da:9d:42:e5:f${macRand}"
  ctMAC2="da:da:9d:42:e5:f${macRand}"

  # Set a known start point config
  ip link set "${parent}" up

  # Test basic container with SR-IOV NIC
  lxc init testimage "${ctName}"
  lxc config device add "${ctName}" eth0 nic \
    nictype=sriov \
    parent="${parent}"
  lxc start "${ctName}"

  # Check spoof checking has been disabled (the default)
  vfID=$(lxc config get "${ctName}" volatile.eth0.last_state.vf.id)
  if ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking on"; then
    echo "spoof checking is still enabled"
    false
  fi

  lxc config device set "${ctName}" eth0 vlan 1234

  # Check custom vlan has been enabled
  vfID=$(lxc config get "${ctName}" volatile.eth0.last_state.vf.id)
  if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "vlan 1234"; then
    echo "vlan not set"
    false
  fi

  lxc config device set "${ctName}" eth0 security.mac_filtering true

  # Check spoof checking has been enabled
  vfID=$(lxc config get "${ctName}" volatile.eth0.last_state.vf.id)
  if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking on"; then
    echo "spoof checking is still disabled"
    false
  fi

  lxc config device set "${ctName}" eth0 vlan 0

  # Check custom vlan has been disabled
  vfID=$(lxc config get "${ctName}" volatile.eth0.last_state.vf.id)
  if ip link show "${parent}" | grep "vf ${vfID}" | grep "vlan"; then
    # Mellanox cards display vlan 0 as vlan 4095!
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "vlan 4095"; then
      echo "vlan still set"
      false
    fi
  fi

  lxc stop "${ctName}"

  # Set custom MAC
  lxc config device set "${ctName}" eth0 hwaddr "${ctMAC1}"
  lxc start "${ctName}"

  # Check custom MAC is applied
  vfID=$(lxc config get "${ctName}" volatile.eth0.last_state.vf.id)
  if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "${ctMAC1}"; then
    echo "eth0 MAC not set"
    false
  fi

  lxc stop "${ctName}"

  # Disable mac filtering and try fresh boot
  lxc config device set "${ctName}" eth0 security.mac_filtering false
  lxc start "${ctName}"

  # Check spoof checking has been disabled (the default)
  vfID=$(lxc config get "${ctName}" volatile.eth0.last_state.vf.id)
  if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking off"; then
    echo "spoof checking is still enabled"
    false
  fi

  # Hot plug fresh device
  lxc config device add "${ctName}" eth1 nic \
    nictype=sriov \
    parent="${parent}" \
    security.mac_filtering=true

  # Check spoof checking has been enabled
  vfID=$(lxc config get "${ctName}" volatile.eth1.last_state.vf.id)
  if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking on"; then
    echo "spoof checking is still disabled"
    false
  fi

  lxc stop "${ctName}"

  # Test setting MAC offline
  lxc config device set "${ctName}" eth1 hwaddr "${ctMAC2}"
  lxc start "${ctName}"

  # Check custom MAC is applied
  vfID=$(lxc config get "${ctName}" volatile.eth1.last_state.vf.id)
  if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "${ctMAC2}"; then
    echo "eth1 MAC not set"
    false
  fi

  lxc stop "${ctName}"
}
