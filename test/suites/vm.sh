_boot_mode() {
  echo "==> VM boot mode combinations"
  lxc launch --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" --config boot.mode=bios
  lxc stop -f v1

  lxc config set v1 boot.mode=uefi-nosecureboot
  lxc start v1
  lxc stop -f v1

  lxc delete -f v1
}

# _nvram_rename checks that an existing VM created by an older snap (which shipped its
# firmware under the OVMF 4MB name) has its NVRAM vars file renamed to the current
# per-architecture name on start, preserving the existing NVRAM state instead of
# regenerating it from the firmware template.
_nvram_rename() {
  echo "==> VM NVRAM vars file rename on firmware name change"

  # Name used by the previous snap for the secureboot vars file on both x86_64 and arm64.
  local old_vars="OVMF_VARS.4MB.ms.fd"
  local inst_dir new_vars old_inode

  lxc launch --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}" --config boot.mode=uefi-secureboot
  lxc stop -f v1

  # The current name is whatever the running snap shipped (e.g. OVMF_VARS_4M.ms.fd on
  # x86_64 or AAVMF_VARS.ms.fd on arm64).
  inst_dir="${LXD_DIR}/virtual-machines/v1"
  new_vars="$(readlink "${inst_dir}/qemu.nvram" || true)"
  if [ -n "${new_vars}" ]; then
    new_vars="$(basename "${new_vars}")"
  fi

  # Manipulating the stopped VM's NVRAM file requires its instance directory to stay reachable
  # from the host while the VM is stopped. Storage backends that unmount the VM's filesystem
  # (config) volume on stop leave qemu.nvram unreachable once stopped, so the
  # read above comes back empty. The rename itself runs during start (volume
  # mounted), is backend-agnostic, and was already exercised by the launch
  # above; only the in-place inode check below needs host access, so skip it
  # when the stopped directory is not reachable.
  if [ -z "${new_vars}" ]; then
    echo "==> SKIP: stopped VM instance directory not reachable on this backend; cannot verify the NVRAM rename in place"
    lxc delete -f v1
    return 0
  fi

  # If the snap still bundles firmware under the old name there is nothing to migrate to yet
  # (the snapcraft.yaml rename has not reached this snap build). The VM still launched above,
  # confirming the new binary boots old-named VMs; skip the rest until the snap is updated.
  if [ "${new_vars}" = "${old_vars}" ]; then
    echo "==> SKIP: snap still bundles firmware as ${old_vars}; rename migration not applicable yet"
    lxc delete -f v1
    return 0
  fi

  sub_test "Existing NVRAM vars file is renamed (not regenerated) on start"

  # Simulate an instance created by a previous snap that used the old firmware name.
  mv "${inst_dir}/${new_vars}" "${inst_dir}/${old_vars}"
  ln -sf "${old_vars}" "${inst_dir}/qemu.nvram"
  old_inode="$(stat -c %i "${inst_dir}/${old_vars}")"

  lxc start v1
  lxc stop -f v1

  # The vars file must have been renamed back to the current name and the symlink updated.
  [ "$(basename "$(readlink "${inst_dir}/qemu.nvram")")" = "${new_vars}" ]
  [ -f "${inst_dir}/${new_vars}" ]
  [ ! -e "${inst_dir}/${old_vars}" ]

  # A matching inode proves the file was renamed (preserving NVRAM state) rather than
  # regenerated from the firmware template.
  [ "$(stat -c %i "${inst_dir}/${new_vars}")" = "${old_inode}" ]

  lxc delete -f v1
}

test_vm_empty() {
  if [ "${LXD_TMPFS:-0}" = "1" ] && ! runsMinimumKernel 6.6; then
    export TEST_UNMET_REQUIREMENT="QEMU requires direct-io support which requires a kernel >= 6.6 for tmpfs support (LXD_TMPFS=${LXD_TMPFS})"
    return 0
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

  echo "==> Percentage memory limits"
  lxc launch --vm --empty v1 -c limits.memory=1% -d "${SMALL_ROOT_DISK}"

  echo "==> Tiny VMs with snapshots"
  lxc stop -f v1
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

  echo "==> Test VM log directory cleanup after deletion"
  [ ! -d "${LXD_DIR}/logs/v1" ]
}

test_vm_pcie_bus() {
  echo "==> Device PCIe bus numbers"
  pool=$(lxc profile device get default root pool)
  orig_volume_size="$(lxc storage get "${pool}" volume.size)"
  if [ -n "${orig_volume_size:-}" ]; then
    echo "==> Override the volume.size to accommodate a large VM"
    lxc storage set "${pool}" volume.size "${SMALLEST_VM_ROOT_DISK}"
  fi

  ensure_import_ubuntu_vm_image

  lxc launch ubuntu-vm v1 --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"

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

  # Check hotplugging volume using PCIe based virtio-blk.
  lxc storage volume create "${pool}" v1block --type=block size=1MiB
  lxc config device add v1 v1block disk source=v1block pool="${pool}"
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
  lxc storage volume delete "${pool}" v1block

  sub_test "Check security.shifted volumes are not remapped by virtiofsd in VMs"
  # VMs do not use user namespaces, so files on a security.shifted volume keep their real
  # on-disk ownership inside the VM (matching containers). virtiofsd must ignore raw.idmap for
  # such volumes, otherwise the file below would appear owned by nobody instead of 123:456.
  lxc config set v1 raw.idmap="both 1000000 0"
  lxc storage volume create "${pool}" v1shift --type=filesystem size=1MiB security.shifted=true
  lxc config device add v1 v1shift disk source=v1shift pool="${pool}" path=/mnt
  lxc start v1
  waitInstanceReady v1
  lxc exec v1 -- findmnt /mnt -t virtiofs
  volPath="${LXD_DIR}/storage-pools/${pool}/custom/default_v1shift"
  touch "${volPath}/shifted-file"
  chown 123:456 "${volPath}/shifted-file"
  [ "$(lxc exec v1 -- stat /mnt/shifted-file -c '%u:%g')" = "123:456" ]
  lxc stop -f v1
  lxc config device remove v1 v1shift
  lxc storage volume delete "${pool}" v1shift
  lxc config unset v1 raw.idmap

  lxc storage volume create "${pool}" v1dir --type=filesystem size=1MiB
  lxc start v1
  lxc config device add v1 mydir disk source=v1dir pool="${pool}" path=/mnt
  [ "$(lxc config get v1 volatile.mydir.bus)" = "7" ]
  waitInstanceReady v1
  setup_instance_gocoverage v1
  lxc exec v1 -- findmnt /mnt -t virtiofs # Check dir is mounted after boot when immediately hot plugged after starting.

  # Coverage data requires clean lxd-agent stop
  prepare_vm_for_hard_stop v1
  lxc restart -f v1 # Check directory share survive a restart.
  waitInstanceReady v1
  lxc exec v1 -- findmnt /mnt -t virtiofs # Check directory is mounted after boot when added before starting.
  lxc config device remove v1 mydir
  [ "$(lxc config get v1 volatile.mydir.bus || echo fail)" = "" ] # Check hot unplug removes bus number.
  ! lxc exec v1 -- findmnt /mnt -t virtiofs || false # Check hot unplug unmounts inside the VM.
  lxc storage volume delete "${pool}" v1dir

  # Check config drive is exported over 9p and virtiofs at boot and check is readonly even when mounted writable.
  lxc exec v1 -- mount -t 9p config /mnt
  ! lxc exec v1 -- touch /mnt/foo || false
  lxc exec v1 -- umount /mnt
  lxc exec v1 -- mount -t virtiofs config /mnt
  ! lxc exec v1 -- touch /mnt/foo || false

  sub_test "Check that limits.max_bus_ports config option enforces the number of allowed PCIe devices"

  # The default value for "limits.max_bus_ports" is 8 ports.
  # Fill all the available slots with PCIe devices.
  local lower_bound=1
  if coverage_enabled; then
    # When coverage is enabled, the GOCOVERDIR is shared between the host and the VM taking one more port.
    lower_bound=2
  fi
  for i in $(seq "${lower_bound}" 5); do
    lxc config device add v1 "aaa${i}" nic nictype=p2p
  done

  # Check that another device cannot be hotplugged because no more available PCIe ports left.
  ! lxc config device add v1 aaa6 nic nictype=p2p || false

  # Stop the VM and attach an additional device.
  lxc stop -f v1
  lxc config device add v1 aaa6 nic nictype=p2p

  # Check that the instance start fails because PCIe devices limit is exceeded.
  ! lxc start v1 || false

  # Increase the PCIe devices limit.
  lxc config set v1 limits.max_bus_ports=10

  # Start the instance, this time there should enough PCIe ports.
  lxc start v1
  waitInstanceReady v1

  # Hotplug an additional device, there should still be one available PCIe port.
  lxc config device add v1 aaa7 nic nictype=p2p

  # Check that another device cannot be hotplugged because no more available PCIe ports left.
  ! lxc config device add v1 aaa8 nic nictype=p2p || false

  # Coverage data requires clean lxd-agent stop
  prepare_vm_for_hard_stop v1
  lxc delete --force v1

  _boot_mode

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

  if [ -n "${orig_volume_size:-}" ]; then
    echo "==> Restore the volume.size"
    lxc storage set "${pool}" volume.size "${orig_volume_size}"
  fi
}

test_snap_vm_empty() {
  # useful to test snap provided BIOS boot
  _boot_mode

  # The NVRAM vars file rename migration only happens inside the snap.
  _nvram_rename
}
