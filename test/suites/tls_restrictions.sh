test_tls_restrictions() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  gen_cert_and_key "${LXD_CONF}/metrics.key" "${LXD_CONF}/metrics.crt" "metrics.local"

  # Ensure type=metrics certificates cannot access anything besides /1.0/metrics.
  curl -k -s --cert "${LXD_CONF}/metrics.crt" --key "${LXD_CONF}/metrics.key" "https://${LXD_ADDR}/1.0/metrics" | grep -F '"error_code":403'
  lxc config trust add "${LXD_CONF}/metrics.crt" --type=metrics
  curl -k -s --cert "${LXD_CONF}/metrics.crt" --key "${LXD_CONF}/metrics.key" "https://${LXD_ADDR}/1.0/metrics" | grep -Fx '# EOF'

  curl -k -s --cert "${LXD_CONF}/metrics.crt" --key "${LXD_CONF}/metrics.key" "https://${LXD_ADDR}/1.0/certificates" | grep -F '"error_code":403'

  # Cleanup type=metrics certificate.
  METRICS_FINGERPRINT="$(lxc config trust list --format csv | grep -F metrics.local | cut -d, -f4)"
  lxc config trust remove "${METRICS_FINGERPRINT}"

  FINGERPRINT="$(lxc config trust list --format csv | cut -d, -f4)"

  # Validate admin rights with no restrictions
  lxc_remote project create localhost:blah

  # Validate normal view with no restrictions
  lxc_remote project list localhost: | grep -q default
  lxc_remote project list localhost: | grep -q blah

  # Apply restrictions
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | lxc config trust edit "${FINGERPRINT}"

  # Confirm no project visible when none listed
  [ "$(lxc_remote project list localhost: --format csv | wc -l)" = 0 ]

  # Confirm we can still view storage pools
  [ "$(lxc_remote storage list localhost: --format csv | wc -l)" = 1 ]

  # Confirm we cannot view storage pool configuration
  pool_name="$(lxc_remote storage list localhost: --format csv | cut -d, -f1)"
  ! lxc_remote storage show "localhost:${pool_name}" | grep -F 'source:' || false

  # Allow access to project blah
  lxc config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: ['blah']/" -e "s/restricted: false/restricted: true/" | lxc config trust edit "${FINGERPRINT}"

  # Validate restricted view
  ! lxc_remote project list localhost: | grep -q default || false
  lxc_remote project list localhost: | grep -q blah

  # Validate that the restricted caller cannot edit or delete the project.
  ! lxc_remote project set localhost:blah user.foo=bar || false
  ! lxc_remote project delete localhost:blah || false

  # Validate restricted caller cannot create projects.
  ! lxc_remote project create localhost:blah1 || false

  # Ensure we can create and view resources that are not enabled for the project (e.g. their effective project is
  # the default project).

  # Networks are disabled when projects are created.
  lxc_remote network create localhost:blah-network --project blah
  lxc_remote network show localhost:blah-network --project blah
  lxc_remote network list localhost: --project blah | grep blah-network
  lxc_remote network rm localhost:blah-network --project blah

  # Network zones are disabled when projects are created.
  lxc_remote network zone create localhost:blah-zone --project blah
  lxc_remote network zone show localhost:blah-zone --project blah
  lxc_remote network zone list localhost: --project blah | grep blah-zone
  lxc_remote network zone delete localhost:blah-zone --project blah

  # Unset the profiles feature (the default is false).
  lxc project unset blah features.profiles
  lxc_remote profile create localhost:blah-profile --project blah
  lxc_remote profile show localhost:blah-profile --project blah
  lxc_remote profile list localhost: --project blah | grep blah-profile
  lxc_remote profile delete localhost:blah-profile --project blah

  # Unset the storage volumes feature (the default is false).
  lxc project unset blah features.storage.volumes
  lxc_remote storage volume create "localhost:${pool_name}" blah-volume --project blah
  lxc_remote storage volume show "localhost:${pool_name}" blah-volume --project blah
  lxc_remote storage volume list "localhost:${pool_name}" --project blah
  lxc_remote storage volume list "localhost:${pool_name}" --project blah | grep blah-volume
  lxc_remote storage volume delete "localhost:${pool_name}" blah-volume --project blah

  # Cleanup
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | lxc config trust edit "${FINGERPRINT}"
  lxc project delete blah
}

test_certificate_edit() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Generate a certificate
  gen_cert_and_key "${LXD_CONF}/client.key.new" "${LXD_CONF}/client.crt.new" "test.local"

  FINGERPRINT="$(lxc config trust list --format csv | cut -d, -f4)"

  # Try replacing the old certificate with a new one.
  # This should succeed as the user is listed as an admin.
  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${LXD_CONF}/client.crt.new")\"}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}"

  # Record new fingerprint
  FINGERPRINT="$(lxc config trust list --format csv | cut -d, -f4)"

  # Move new certificate and key to LXD_CONF and back up old files.
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
  mv "${LXD_CONF}/client.crt.new" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.new" "${LXD_CONF}/client.key"

  lxc_remote project create localhost:blah

  # Apply restrictions
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | lxc config trust edit "${FINGERPRINT}"

  # Add created project to the list of restricted projects. This way, the user will be listed as
  # a normal user instead of an admin.
  lxc config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: \[blah\]/" | lxc config trust edit "${FINGERPRINT}"

  # Try replacing the new certificate with the old one.
  # This should succeed as well as the certificate may be changed.
  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${LXD_CONF}/client.crt.bak")\"}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}"

  # Move new certificate and key to LXD_CONF.
  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"

  # Record new fingerprint
  FINGERPRINT="$(lxc config trust list --format csv | cut -d, -f4)"

  # Trying to change other fields should fail as a non-admin.
  ! lxc_remote config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | lxc_remote config trust edit localhost:"${FINGERPRINT}" || false

  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" -X PATCH -d "{\"restricted\": false}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

  ! lxc_remote config trust show "${FINGERPRINT}" | sed -e "s/name:.*/name: foo/" | lxc_remote config trust edit localhost:"${FINGERPRINT}" || false

  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" -X PATCH -d "{\"name\": \"bar\"}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

  ! lxc_remote config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n- blah/projects: \[\]/' | lxc_remote config trust edit localhost:"${FINGERPRINT}" || false

  curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" -X PATCH -d "{\"projects\": []}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

  # Cleanup
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | lxc config trust edit "${FINGERPRINT}"

  lxc config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n- blah/projects: \[\]/' | lxc config trust edit "${FINGERPRINT}"

  lxc project delete blah
}
