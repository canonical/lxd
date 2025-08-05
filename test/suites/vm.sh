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

  lxc launch --vm --empty v1 -c limits.memory=1% -d "${SMALL_ROOT_DISK}"
  lxc delete --force v1

  echo "==> Ephemeral cleanup"
  lxc launch --vm --empty --ephemeral v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc stop -f v1
  [ "$(lxc list -f csv -c n)" = "" ]

  if grep -qxF 'VERSION_ID="22.04"' /etc/os-release; then
    # Cleanup custom changes from the default profile
    lxc profile unset default migration.stateful
  fi

  # This check must be run after unsetting "migration.stateful" to workaround 9p limitations; shared filesystems are incompatible.
  echo "==> Disk mounts"
  lxc init --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc config device add v1 char disk source=/dev/zero path=/dev/zero
  # Attempting to mount a single file as a disk device is not supported for VMs; this should fail at start time.
  ! lxc start v1 || false
  lxc delete v1
}
