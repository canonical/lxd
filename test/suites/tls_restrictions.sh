test_tls_restrictions() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  FINGERPRINT=$(lxc config trust list --format csv | cut -d, -f4)

  # Validate admin rights with no restrictions
  lxc_remote project create localhost:blah

  # Validate normal view with no restrictions
  lxc_remote project list localhost: | grep -q default
  lxc_remote project list localhost: | grep -q blah

  # Apply restrictions
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | lxc config trust edit "${FINGERPRINT}"

  # Confirm no project visible when none listed
  [ "$(lxc_remote project list localhost: --format csv | wc -l)" = 0 ]

  # Allow access to project blah
  lxc config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: ['blah']/" -e "s/restricted: false/restricted: true/" | lxc config trust edit "${FINGERPRINT}"

  # Validate restricted view
  ! lxc_remote project list localhost: | grep -q default || false
  lxc_remote project list localhost: | grep -q blah

  ! lxc_remote project create localhost:blah1 || false

  # Cleanup
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | lxc config trust edit "${FINGERPRINT}"
  lxc project delete blah
}
