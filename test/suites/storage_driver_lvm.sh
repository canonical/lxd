do_storage_driver_lvm_image_recovery() {
  local pool vg_name fingerprint
  pool="lxdtest-$(basename "${LXD_DIR}")-pool1"
  vg_name="${pool}"

  sub_test "Verify a corrupted image volume is automatically regenerated before instance creation."

  ensure_import_testimage
  fingerprint=$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')

  # Trigger EnsureImage to create the optimised LVM image volume (LV + readonly snapshot).
  # Unlike ZFS/btrfs, LVM volumes are created lazily on first use, not at import time.
  lxc init testimage c-populate
  lxc delete c-populate

  # Simulate an aborted image volume creation by removing the thinpool finalization snapshot.
  # The readonly snapshot LV name uses "-" as separator: images_<fingerprint>-readonly.
  # Use VG/LV notation so that lvremove works regardless of LV activation state.
  lvremove --force "${vg_name}/images_${fingerprint}-readonly"

  # The image integrity check detects the missing snapshot and
  # regenerates the image volume before the instance is created.
  lxc init testimage c-recovery

  # Verify the readonly snapshot LV was recreated as part of the regeneration.
  # Use lvs so the check works regardless of LV activation state.
  lvs "${vg_name}/images_${fingerprint}-readonly"

  lxc delete c-recovery
  lxc image delete testimage

  # Test VM image recovery (if VM tests are enabled).
  if [ "${LXD_VM_TESTS:-}" = "true" ]; then
    sub_test "Verify a corrupted VM image volume is automatically regenerated before instance creation."

    ensure_import_ubuntu_vm_image
    fingerprint=$(lxc image info ubuntu-vm | awk '/^Fingerprint/ {print $2}')

    # Trigger EnsureImage to create the optimised LVM VM image volumes before simulating corruption.
    lxc init ubuntu-vm vm-populate --vm
    lxc delete vm-populate

    # Simulate an aborted VM image volume creation by removing the readonly snapshots.
    # VMs have two LVs: images_<fingerprint> (filesystem config) and images_<fingerprint>.block (block device).
    # Each has a -readonly snapshot that serves as the finalization marker.
    lvremove --force "${vg_name}/images_${fingerprint}.block-readonly"
    lvremove --force "${vg_name}/images_${fingerprint}-readonly"

    # The image integrity check should detect the missing readonly snapshots
    # and regenerate the complete VM image structure before instance creation.
    lxc init ubuntu-vm vm-recovery --vm

    # Verify both readonly snapshots were recreated as part of the regeneration.
    lvs "${vg_name}/images_${fingerprint}.block-readonly"
    lvs "${vg_name}/images_${fingerprint}-readonly"

    # Verify the instance root disk volumes were properly created.
    lvs "${vg_name}/virtual-machine_vm-recovery.block"
    lvs "${vg_name}/virtual-machine_vm-recovery"

    lxc delete vm-recovery --force
    lxc image delete ubuntu-vm
  fi
}

test_storage_driver_lvm() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" != "lvm" ]; then
    export TEST_UNMET_REQUIREMENT="lvm specific test, not for ${lxd_backend}"
    return
  fi

  local LXD_STORAGE_DIR LXD_DIR_ORIG
  LXD_DIR_ORIG="${LXD_DIR}"

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    lxc storage create "lxdtest-$(basename "${LXD_DIR}")-pool1" lvm volume.size="${DEFAULT_VOLUME_SIZE}"

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    do_storage_driver_lvm_image_recovery
  )

  # shellcheck disable=2031
  LXD_DIR="${LXD_STORAGE_DIR}"
  kill_lxd "${LXD_DIR}"

  # Restore the original LXD_DIR so subsequent tests can connect to the main LXD instance.
  LXD_DIR="${LXD_DIR_ORIG}"
}
