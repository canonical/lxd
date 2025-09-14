test_container_devices_nic_macvlan() {
  ensure_import_testimage

  ctName="nt$$"
  ipRand=$(shuf -i 0-9 -n 1)

  echo "==> Create dummy interface for use as parent."
  ip link add "${ctName}" type dummy
  ip link set "${ctName}" up

  echo "==> Record how many nics we started with."
  startNicCount=$(find /sys/class/net | wc -l)

  echo "==> Test pre-launch profile config is applied at launch."

  # Create profile for new containers by atomically modifying nictype and parent to ensure validation passes.
  lxc profile show default | sed  "s/nictype: p2p/nictype: macvlan\\n    parent: ${ctName}/" | lxc profile create "${ctName}"

  lxc profile device set "${ctName}" eth0 mtu "1400"

  lxc launch testimage "${ctName}" -p "${ctName}"
  lxc exec "${ctName}" -- ip addr add "192.0.2.1${ipRand}/24" dev eth0
  lxc exec "${ctName}" -- ip addr add "2001:db8::1${ipRand}/64" dev eth0

  echo "==> Check custom MTU is applied if feature available in LXD."
  if ! lxc exec "${ctName}" -- ip link show eth0 | grep "mtu 1400" ; then
    echo "mtu invalid"
    false
  fi

  echo "==> Spin up another container with multiple IPs."
  lxc launch testimage "${ctName}2" -p "${ctName}"
  lxc exec "${ctName}2" -- ip addr add "192.0.2.2${ipRand}/24" dev eth0
  lxc exec "${ctName}2" -- ip addr add "2001:db8::2${ipRand}/64" dev eth0

  wait_for_dad "${ctName}" eth0
  wait_for_dad "${ctName}2" eth0

  echo "==> Check comms between containers."
  lxc exec "${ctName}" -- ping -nc2 -i0.1 -W1 "192.0.2.2${ipRand}"
  lxc exec "${ctName}" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::2${ipRand}"
  lxc exec "${ctName}2" -- ping -nc2 -i0.1 -W1 "192.0.2.1${ipRand}"
  lxc exec "${ctName}2" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::1${ipRand}"

  echo "==> Test hot plugging a container nic with different settings to profile with the same name."
  lxc config device add "${ctName}" eth0 nic \
    nictype=macvlan \
    name=eth0 \
    parent="${ctName}" \
    mtu=1401

  echo "==> Check custom MTU is applied on hot-plug."
  if ! lxc exec "${ctName}" -- ip link show eth0 | grep "mtu 1401" ; then
    echo "mtu invalid"
    false
  fi

  echo "==> Check that MTU is inherited from parent device when not specified on device."
  ip link set "${ctName}" mtu 1405
  lxc config device unset "${ctName}" eth0 mtu
  if [ "$(lxc exec "${ctName}" -- cat /sys/class/net/eth0/mtu)" != "1405" ]; then
    echo "mtu not inherited from parent"
    false
  fi

  echo "==> Check volatile cleanup on stop."
  lxc stop -f "${ctName}"
  if [ "$(lxc config show "${ctName}" | grep -F volatile.eth0 | grep -vF volatile.eth0.hwaddr)" != "" ]; then
    echo "unexpected volatile key remains"
    false
  fi

  lxc start "${ctName}"
  lxc config device remove "${ctName}" eth0

  echo "==> Test hot plugging macvlan device based on vlan parent."
  lxc config device add "${ctName}" eth0 nic \
    nictype=macvlan \
    parent="${ctName}" \
    name=eth0 \
    vlan=10 \
    mtu=1402

  echo "==> Check custom MTU is applied."
  if ! lxc exec "${ctName}" -- ip link show eth0 | grep "mtu 1402" ; then
    echo "mtu invalid"
    false
  fi

  echo "==> Check VLAN interface created."
  if [ "$(< "/sys/class/net/${ctName}.10/carrier")" != "1" ]; then
    echo "vlan interface not created"
    false
  fi

  echo "==> Remove device from container, this should also remove created VLAN parent device."
  lxc config device remove "${ctName}" eth0

  echo "==> Check parent device is still up."
  if [ "$(< "/sys/class/net/${ctName}/carrier")" != "1" ]; then
    echo "parent is down"
    false
  fi

  echo "==> Test using macvlan network."

  echo "==> Create macvlan network."
  lxc network create "${ctName}net" --type=macvlan parent="${ctName}"

  echo "==> Check that lxc network info succeeds for macvlan network."
  lxc network info "${ctName}net"

  echo "==> Check that macvlan network info shows parent's MTU by default."
  parentMTU=$(</sys/class/net/"${ctName}"/mtu)
  if ! lxc network info "${ctName}net" | grep -xF "MTU: ${parentMTU}" ; then
    echo "default mtu not inherited from parent"
    false
  fi

  echo "==> Set a valid MTU config value for macvlan network."
  customMTU=1492
  lxc network set "${ctName}net" mtu="${customMTU}"

  echo "==> Check that network info shows correct MTU value for macvlan network."
  if ! lxc network info "${ctName}net" | grep -xF "MTU: ${customMTU}" ; then
    echo "config mtu ignored"
    false
  fi

  echo "==> Check that setting MTU config value out of range (1280-16384) is not allowed."
  ! lxc network set "${ctName}net" mtu=0 || false
  ! lxc network set "${ctName}net" mtu=1000 || false
  ! lxc network set "${ctName}net" mtu=1279 || false
  ! lxc network set "${ctName}net" mtu=16385 || false
  ! lxc network set "${ctName}net" mtu=50000 || false

  echo "==> Check that setting MTU config value to the boundaries of range (1280-16384) is allowed."
  lxc network set "${ctName}net" mtu=1280
  lxc network set "${ctName}net" mtu=16384

  echo "==> Unset MTU config value for macvlan network."
  lxc network unset "${ctName}net" mtu

  echo "==> Check that macvlan network info falls back to using parent's MTU value."
  if ! lxc network info "${ctName}net" | grep -xF "MTU: ${parentMTU}" ; then
    echo "default mtu not inherited from parent"
    false
  fi

  echo "==> Add NIC device using macvlan network."
  lxc config device add "${ctName}" eth0 nic \
    network="${ctName}net" \
    name=eth0
  lxc exec "${ctName}" -- ip addr add "192.0.2.1${ipRand}/24" dev eth0
  lxc exec "${ctName}" -- ip addr add "2001:db8::1${ipRand}/64" dev eth0
  lxc exec "${ctName}" -- ip link set eth0 up
  wait_for_dad "${ctName}" eth0
  lxc exec "${ctName}" -- ping -nc2 -i0.1 -W1 "192.0.2.2${ipRand}"
  lxc exec "${ctName}" -- ping -6 -nc2 -i0.1 -W1 "2001:db8::2${ipRand}"
  lxc config device remove "${ctName}" eth0
  lxc network delete "${ctName}net"

  echo "==> Check we haven't left any NICS lying around."
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  echo "==> Cleanup."
  lxc delete "${ctName}" -f
  lxc delete "${ctName}2" -f
  lxc profile delete "${ctName}"
  ip link delete "${ctName}" type dummy
}
