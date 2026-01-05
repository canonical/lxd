test_container_devices_tpm() {
  if ! modprobe tpm_vtpm_proxy; then
    if [[ "$(uname -r)" =~ -azure$ ]]; then
      export TEST_UNMET_REQUIREMENT="Required tpm_vtpm_proxy.ko is missing"
      return 0
    fi

    install_packages "linux-modules-extra-$(uname -r)"
    modprobe tpm_vtpm_proxy
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

  # Check that no swtpm process is left behind
  if pgrep -x swtpm; then
    echo "::error:: 'swtpm' process left behind pointing to invalid cleanup"

    echo "::info:: Workaround for https://github.com/canonical/lxd/issues/16569"
    pkill -x swtpm || exit 1

    check_empty "${LXD_DIR}/devices/" || echo "::info::Ignoring device leftovers"
  fi

  # Clean up
  lxc delete -f "${ctName}"
}
