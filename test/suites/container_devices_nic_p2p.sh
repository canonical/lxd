test_container_devices_nic_p2p() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  vethHostName="veth$$"
  ctName="nt$$"
  ctMAC="0A:92:a7:0d:b7:D9"
  ipRand=$(shuf -i 0-9 -n 1)

  # Test pre-launch profile config is applied at launch.
  lxc profile copy default ${ctName}
  lxc profile device set ${ctName} eth0 limits.ingress 1Mbit
  lxc profile device set ${ctName} eth0 limits.egress 2Mbit
  lxc profile device set ${ctName} eth0 host_name "${vethHostName}"
  lxc profile device set ${ctName} eth0 mtu "1400"
  lxc profile device set ${ctName} eth0 hwaddr "${ctMAC}"
  lxc profile device set ${ctName} eth0 nictype "p2p"
  lxc launch testimage "${ctName}" -p ${ctName}

  # Check profile limits are applied on boot.
  if ! tc class show dev "${vethHostName}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied in container on boot.
  if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check profile custom MAC is applied in container on boot.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Test hot plugging a container nic with different settings to profile with the same name.
  lxc config device add "${ctName}" eth0 nic \
    nictype=p2p \
    name=eth0 \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    host_name="${vethHostName}p2p" \
    hwaddr="${ctMAC}" \
    mtu=1401

  # Check limits are applied on hot-plug.
  if ! tc class show dev "${vethHostName}p2p" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}p2p" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied on hot-plug.
  if ! lxc exec "${ctName}" -- grep "1401" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check custom MAC is applied on hot-plug.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Test removing hot plugged device and check profile nic is restored.
  lxc config device remove "${ctName}" eth0

  if ! tc class show dev "${vethHostName}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi

  # Check profile limits are applied on hot-removal.
  if ! tc filter show dev "${vethHostName}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied on hot-removal.
  if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Test hot plugging a container nic then updating it.
  lxc config device add "${ctName}" eth0 nic \
    nictype=p2p \
    name=eth0 \
    host_name="${vethHostName}"

  lxc config device set "${ctName}" eth0 limits.ingress 3Mbit
  lxc config device set "${ctName}" eth0 limits.egress 4Mbit
  lxc config device set "${ctName}" eth0 mtu 1402
  lxc config device set "${ctName}" eth0 hwaddr "${ctMAC}"

  # Check limits are applied on update.
  if ! tc class show dev "${vethHostName}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied update.
  if ! lxc exec "${ctName}" -- grep "1402" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check custom MAC is applied update.
  if ! lxc exec "${ctName}" -- grep -i "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Cleanup.
  lxc config device remove "${ctName}" eth0
  lxc delete "${ctName}" -f
  lxc profile delete "${ctName}"

  # Test adding a p2p device to a running container without host_name and no limits/routes.
  lxc launch testimage "${ctName}"
  lxc config device add "${ctName}" eth0 nic \
    nictype=p2p
  lxc config device remove "${ctName}" eth0
  lxc delete "${ctName}" -f
}
