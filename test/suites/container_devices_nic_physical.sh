test_container_devices_nic_physical() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  ctName="nt$$"
  dummyMAC="aa:3b:97:97:0f:d5"
  ctMAC="0a:92:a7:0d:b7:d9"

  # Create dummy interface for use as parent.
  ip link add "${ctName}" address "${dummyMAC}" type dummy

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Create test container from default profile.
  lxc init testimage "${ctName}"

  # Add physical device to container/
  lxc config device add "${ctName}" eth0 nic \
    nictype=physical \
    parent="${ctName}" \
    name=eth0 \
    mtu=1400 \
    hwaddr="${ctMAC}"

  # Launch container and check it has nic applied correctly.
  lxc start "${ctName}"

  # Check custom MTU is applied if feature available in LXD.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check custom MAC is applied in container.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Check volatile cleanup on stop.
  lxc stop -f "${ctName}"
  if lxc config show "${ctName}" | grep volatile.eth0 ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Check original MTU is restored on physical device.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! grep "1500" /sys/class/net/"${ctName}"/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check original MAC is restored on physical device.
   if ! grep -i "${dummyMAC}" /sys/class/net/"${ctName}"/address ; then
    echo "mac invalid"
    false
  fi

  # Remove boot time physical device and check MTU is restored.
  lxc start "${ctName}"
  lxc config device remove "${ctName}" eth0

  # Check original MTU is restored on physical device.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! grep "1500" /sys/class/net/"${ctName}"/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check original MAC is restored on physical device.
   if ! grep -i "${dummyMAC}" /sys/class/net/"${ctName}"/address ; then
    echo "mac invalid"
    false
  fi

  # Test hot-plugging physical device based on vlan parent.
  # Make the MTU higher than the original boot time 1400 MTU above to check that the
  # parent device's MTU is reset on removal to the pre-boot value on host (expect >=1500).
  ip link set "${ctName}" up #VLAN requires parent nic be up.
  lxc config device add "${ctName}" eth0 nic \
    nictype=physical \
    parent="${ctName}" \
    name=eth0 \
    vlan=10 \
    hwaddr="${ctMAC}" \
    mtu=1401 #Higher than 1400 boot time value above

  # Check custom MTU is applied if feature available in LXD.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! lxc exec "${ctName}" -- grep "1401" /sys/class/net/eth0/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check custom MAC is applied in container.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Remove hot-plugged physical device and check MTU is restored.
  lxc config device remove "${ctName}" eth0

  # Check original MTU is restored on physical device.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! grep "1500" /sys/class/net/"${ctName}"/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check original MAC is restored on physical device.
   if ! grep -i "${dummyMAC}" /sys/class/net/"${ctName}"/address ; then
    echo "mac invalid"
    false
  fi

  # Test hot-plugging physical device based on existing parent.
  # Make the MTU higher than the original boot time 1400 MTU above to check that the
  # parent device's MTU is reset on removal to the pre-boot value on host (expect >=1500).
  lxc config device add "${ctName}" eth0 nic \
    nictype=physical \
    parent="${ctName}" \
    name=eth0 \
    hwaddr="${ctMAC}" \
    mtu=1402 #Higher than 1400 boot time value above

  # Check custom MTU is applied if feature available in LXD.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! lxc exec "${ctName}" -- grep "1402" /sys/class/net/eth0/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check custom MAC is applied in container.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Test removing a physical device an check its MTU gets restored to default 1500 mtu
  lxc config device remove "${ctName}" eth0

  # Check original MTU is restored on physical device.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! grep "1500" /sys/class/net/"${ctName}"/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check original MAC is restored on physical device.
   if ! grep -i "${dummyMAC}" /sys/class/net/"${ctName}"/address ; then
    echo "mac invalid"
    false
  fi

  # Test hot-plugging physical device based on existing parent with new name that LXC doesn't know about.
  lxc config device add "${ctName}" eth1 nic \
    nictype=physical \
    parent="${ctName}" \
    hwaddr="${ctMAC}" \
    mtu=1402 #Higher than 1400 boot time value above

  # Stop the container, LXC doesn't know about the nic, so we will rely on LXD to restore it.
  lxc stop -f "${ctName}"

  # Check original MTU is restored on physical device.
  if lxc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! grep "1500" /sys/class/net/"${ctName}"/mtu ; then
      echo "mtu invalid"
      false
    fi
  fi

  # Check original MAC is restored on physical device.
   if ! grep -i "${dummyMAC}" /sys/class/net/"${ctName}"/address ; then
    echo "mac invalid"
    false
  fi

  lxc delete "${ctName}"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Remove dummy interface (should still exist).
  ip link delete "${ctName}"
}
