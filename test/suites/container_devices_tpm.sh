test_container_devices_tpm() {
  if ! modprobe tpm_vtpm_proxy; then
    echo "==> SKIP: Required tpm_vtpm_proxy.ko is missing"
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
