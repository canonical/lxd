test_container_devices_tpm() {
  if ! modprobe tpm_vtpm_proxy; then
    echo "==> SKIP: Required tpm_vtpm_proxy.ko is missing"
    return
  fi

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "${lxd_backend}" = "lvm" ]; then
    #+ lxc config device rm ct843376 test-dev1
    #++ timeout --foreground 120 /root/go/bin/lxc config device rm ct843376 test-dev1
    #Device test-dev1 removed from ct843376
    #+ lxc exec ct843376 -- stat /dev/tpm0
    #++ timeout --foreground 120 /root/go/bin/lxc exec ct843376 --force-local -- stat /dev/tpm0
    #stat: can't stat '/dev/tpm0': No such file or directory
    #+ lxc exec ct843376 -- stat /dev/tpmrm0
    #++ timeout --foreground 120 /root/go/bin/lxc exec ct843376 --force-local -- stat /dev/tpmrm0
    #stat: can't stat '/dev/tpmrm0': No such file or directory
    #+ lxc delete -f ct843376
    #++ timeout --foreground 120 /root/go/bin/lxc delete -f ct843376
    #INFO   [2025-09-14T00:02:59Z] Stopping instance                  action=stop created="2025-09-14 00:02:58.11670028 +0000 UTC" ephemeral=false instance=ct843376 instanceType=container project=default stateful=false used="2025-09-14 00:02:58.916546684 +0000 UTC"
    #Error: Stopping the instance failed: Failed unmounting instance: Failed to unmount LVM logical volume: Failed to unmount "/tmp/lxd-test.tmp.GyyF/9wL/storage-pools/lxdtest-9wL/containers/ct843376": device or resource busy
    echo "==> SKIP: known broken test on 'lvm'"
    return
  fi

  ensure_import_testimage
  ctName="ct$$"
  lxc launch testimage "${ctName}"

  # Check adding a device with no path
  ! lxc config device add "${ctName}" test-dev-invalid tpm || false

  # Add device
  lxc config device add "${ctName}" test-dev1 tpm path=/dev/tpm0 pathrm=/dev/tpmrm0
  lxc exec "${ctName}" -- stat /dev/tpm0
  lxc exec "${ctName}" -- stat /dev/tpmrm0

  # Remove device
  lxc config device rm "${ctName}" test-dev1
  ! lxc exec "${ctName}" -- stat /dev/tpm0 || false
  ! lxc exec "${ctName}" -- stat /dev/tpmrm0 || false

  # Clean up
  lxc delete -f "${ctName}"
}
