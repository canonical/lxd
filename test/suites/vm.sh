test_vm_empty() {
  if [ "${LXD_VM_TESTS:-0}" = "0" ]; then
    echo "==> SKIP: VM tests are disabled"
    return
  fi

  if [ "${LXD_TMPFS:-0}" = "1" ] && ! runsMinimumKernel 6.6; then
    echo "==> SKIP: QEMU requires direct-io support which requires a kernel >= 6.6 for tmpfs support (LXD_TMPFS=${LXD_TMPFS})"
    return
  fi

  if grep -qxF 'VERSION_ID="22.04"' /etc/os-release; then
    echo "Using migration.stateful to force 9p config drive thus avoiding the old/incompatible virtiofsd"
    lxc profile set default migration.stateful=true
  fi

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
  lxc delete --force v1

  echo "==> Percentage memory limits"
  lxc launch --vm --empty v1 -c limits.memory=1% -d "${SMALL_ROOT_DISK}"

  echo "==> Device PCIe bus numbers"
  # Check initial boot bus allocation.
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]

  # Hotplug device and check bus allocation.
  lxc config device add v1 aaa0 nic nictype=p2p
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]

  lxc stop -f v1

  # Make the root disk device to consume a PCIe slot and check bus allocation.
  lxc config device set v1 root io.bus=nvme
  lxc start v1
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  [ "$(lxc config get v1 volatile.root.bus)" = "6" ]

  # Check with virtio-blk bus after reboot.
  lxc stop v1 -f
  lxc config device set v1 root io.bus=virtio-blk
  lxc start v1
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  [ "$(lxc config get v1 volatile.root.bus)" = "6" ]

  # Check hotplugging block volume.
  poolName=$(lxc profile device get default root pool)
  lxc storage volume create "${poolName}" v1block --type=block size=1MiB
  lxc config device add v1 v1block disk source=v1block pool="${poolName}"
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  [ "$(lxc config get v1 volatile.root.bus)" = "6" ]
  [ "$(lxc config get v1 volatile.v1block.bus)" = "" ] # Uses SCSI by default so no PCIe bus number
  lxc config device set v1 v1block io.bus=nvme
  [ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
  [ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  [ "$(lxc config get v1 volatile.root.bus)" = "6" ]
  [ "$(lxc config get v1 volatile.v1block.bus)" = "7" ]

  # Check re-use of the PCIe bus number when modifying device causes remove & re-add.
  #lxc config device set v1 aaa0 queue.tx.length=1024
  #[ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
  #[ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  #[ "$(lxc config get v1 volatile.root.bus)" = "6" ]
  #[ "$(lxc config get v1 volatile.v1block.bus)" = "7" ]

  #lxc config device set v1 v1block io.bus=virtio-blk
  #[ "$(lxc config get v1 volatile.eth0.bus)" = "4" ]
  #[ "$(lxc config get v1 volatile.aaa0.bus)" = "5" ]
  #[ "$(lxc config get v1 volatile.root.bus)" = "6" ]
  #[ "$(lxc config get v1 volatile.v1block.bus)" = "7" ] # Check re-use when modifying device causes remove & re-add.

  lxc delete --force v1
  lxc storage volume delete "${poolName}" v1block

  echo "==> Ephemeral cleanup"
  lxc launch --vm --empty --ephemeral v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc stop -f v1
  [ "$(lxc list -f csv -c n)" = "" ]

  if grep -qxF 'VERSION_ID="22.04"' /etc/os-release; then
    # Cleanup custom changes from the default profile
    lxc profile unset default migration.stateful
  fi
}
