_secureboot_csm_boot() {
  echo "==> Secure boot and CSM combinations"
  # CSM requires secureboot to be disabled.
  ! lxc launch --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" --config security.csm=true || false
  lxc config set v1 security.secureboot=false
  lxc start v1
  lxc stop -f v1
  # CSM with secureboot should refuse to start.
  lxc config set v1 security.secureboot=true
  ! lxc start v1 || false
  lxc delete -f v1
}

test_vm_empty() {
  if [ "${LXD_TMPFS:-0}" = "1" ] && ! runsMinimumKernel 6.6; then
    echo "==> SKIP: QEMU requires direct-io support which requires a kernel >= 6.6 for tmpfs support (LXD_TMPFS=${LXD_TMPFS})"
    return
  fi

  echo "==> Test randomly named VM creation"
  RDNAME="$(lxc init --vm --empty --quiet -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" | sed 's/Instance name is: //')"
  lxc delete "${RDNAME}"

  echo "==> Invalid VM names"
  ! lxc init --vm --empty ".." -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" || false
  # Escaping `\` multiple times due to `lxc` wrapper script munging the first layer
  ! lxc init --vm --empty "\\\\" -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" || false
  ! lxc init --vm --empty "/" -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" || false
  ! lxc init --vm --empty ";" -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" || false

  echo "==> Too small VMs"
  ! lxc launch --vm --empty v1 -c limits.memory=0 -d "${SMALL_ROOT_DISK}" || false
  ! lxc launch --vm --empty v1 -c limits.memory=0% -d "${SMALL_ROOT_DISK}" || false

  echo "==> Tiny VMs with snapshots"
  lxc init --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc snapshot v1
  # Invalid snapshot names
  ! lxc snapshot v1 ".." || false
  # Escaping `\` multiple times due to `lxc` wrapper script munging the first layer
  ! lxc snapshot v1 "\\\\" || false
  ! lxc snapshot v1 "/" || false
  [ "$(lxc list -f csv -c S v1)" = "1" ]
  lxc start v1
  lxc snapshot v1
  [ "$(lxc list -f csv -c S v1)" = "2" ]

  echo "==> Check VM state transitions"
  [ "$(lxc list -f csv -c s v1)" = "RUNNING" ]
  lxc pause v1
  [ "$(lxc list -f csv -c s v1)" = "FROZEN" ]
  ! lxc stop v1 || false
  [ "$(lxc list -f csv -c s v1)" = "FROZEN" ]
  lxc start v1
  [ "$(lxc list -f csv -c s v1)" = "RUNNING" ]
  lxc stop -f v1
  [ "$(lxc list -f csv -c s v1)" = "STOPPED" ]
  lxc delete v1

  echo "==> Percentage memory limits"
  lxc launch --vm --empty v1 -c limits.memory=1% -d "${SMALL_ROOT_DISK}"

  echo "==> Device PCIe bus numbers"
  # Check initial boot bus allocation.
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]

  # Hotplug device and check bus allocation.
  lxc config device add v1 aaa0 nic nictype=p2p
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]

  # Check that the NIC devices are maintained when restarted.
  lxc restart -f v1
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]

  lxc stop -f v1

  # Make the root disk device to consume a PCIe slot and check bus allocation.
  lxc config device set v1 root io.bus=virtio-blk
  lxc start v1
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  [ "$(lxc config get v1 volatile.root.bus)" = "6" ]

  # Check hotplugging block volume.
  poolName=$(lxc profile device get default root pool)
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  if [ "$(lxc config get --expanded v1 migration.stateful || echo fail)" = "" ] || [ "${poolDriver}" = "ceph" ]; then
    # Check using PCIe based virtio-blk when using shared storage or migration.stateful disabled.
    lxc storage volume create "${poolName}" v1block --type=block size=1MiB
    lxc config device add v1 v1block disk source=v1block pool="${poolName}"
    [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
    [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
    [ "$(lxc config get v1 volatile.root.bus)" = "6" ]
    [ "$(lxc config get v1 volatile.v1block.bus || echo fail)" = "" ] # Uses virtio-scsi by default so no PCIe bus number
    lxc config device set v1 v1block io.bus=virtio-blk
    [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
    [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
    [ "$(lxc config get v1 volatile.root.bus)" = "6" ]
    [ "$(lxc config get v1 volatile.v1block.bus)" = "7" ]
    lxc stop -f v1
    lxc config device remove v1 v1block
    lxc storage volume delete "${poolName}" v1block
  fi

  lxc delete --force v1

  _secureboot_csm_boot

  echo "==> Ephemeral cleanup"
  lxc launch --vm --empty --ephemeral v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc stop -f v1
  [ "$(lxc list -f csv -c n || echo fail)" = "" ]

  echo "==> Disk mounts"
  lxc init --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc config device add v1 char disk source=/dev/zero path=/dev/zero
  # Attempting to mount a single file as a disk device is not supported for VMs; this should fail at start time.
  ! lxc start v1 || false
  lxc delete v1
}

test_snap_vm_empty() {
  # useful to test snap provided CSM BIOS
  _secureboot_csm_boot
}
