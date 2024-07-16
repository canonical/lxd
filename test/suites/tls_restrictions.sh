test_tls_restrictions() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  gen_cert_and_key "metrics"

  # Ensure type=metrics certificates cannot access anything besides /1.0/metrics.
  CERTNAME=metrics my_curl "https://${LXD_ADDR}/1.0/metrics" | grep -F '"error_code":403'
  lxc config trust add "${LXD_CONF}/metrics.crt" --type=metrics
  CERTNAME=metrics my_curl "https://${LXD_ADDR}/1.0/metrics" | grep -Fx '# EOF'

  CERTNAME=metrics my_curl "https://${LXD_ADDR}/1.0/certificates" | grep -F '"error_code":403'

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

  # Validate restricted caller cannot list resources in projects they do not have access to
  ! lxc_remote list localhost: --project default || false
  ! lxc_remote profile list localhost: --project default || false
  ! lxc_remote network list localhost: --project default || false
  ! lxc_remote operation list localhost: --project default || false
  ! lxc_remote network zone list localhost: --project default || false
  ! lxc_remote storage volume list "localhost:${pool_name}" --project default || false
  ! lxc_remote storage bucket list "localhost:${pool_name}" --project default || false

  ### Validate images.
  test_image_fingerprint="$(lxc image info testimage --project default | awk '/^Fingerprint/ {print $2}')"

  # We can always list images, but there are no public images in the default project now, so the list should be empty.
  [ "$(lxc_remote image list localhost: --project default --format csv)" = "" ]
  ! lxc_remote image show localhost:testimage --project default || false

  # Set the image to public and ensure we can view it.
  lxc image show testimage --project default | sed -e "s/public: false/public: true/" | lxc image edit testimage --project default
  [ "$(lxc_remote image list localhost: --project default --format csv | wc -l)" = 1 ]
  lxc_remote image show localhost:testimage --project default

  # Check we can export the public image:
  lxc image export localhost:testimage "${LXD_DIR}/" --project default
  [ "${test_image_fingerprint}" = "$(sha256sum "${LXD_DIR}/${test_image_fingerprint}.tar.xz" | cut -d' ' -f1)" ]
  rm "${LXD_DIR}/${test_image_fingerprint}.tar.xz"

  # While the image is public, copy it to the blah project and create an alias for it.
  lxc_remote image copy localhost:testimage localhost: --project default --target-project blah
  lxc_remote image alias create localhost:testimage "${test_image_fingerprint}" --project blah

  # Restore privacy on the test image in the default project.
  lxc image show testimage --project default | sed -e "s/public: true/public: false/" | lxc image edit testimage --project default

  # Set up a profile in the blah project. Additionally ensures restricted TLS clients can edit profiles in projects they have access to.
  lxc profile show default | lxc_remote profile edit localhost:default --project blah

  # Create an instance (using the test image copied from the default project while it was public).
  lxc_remote init testimage localhost:blah-instance --project blah

  # Create a custom volume.
  lxc_remote storage volume create "localhost:${pool_name}" blah-volume --project blah

  # There should now be two volume URLs, one instance, one image, and one profile URL in the used-by list.
  [ "$(lxc_remote project list localhost: --format csv | cut -d, -f9)" = "5" ]

  # Delete resources in project blah so that we can modify project limits.
  lxc_remote delete localhost:blah-instance --project blah
  lxc_remote storage volume delete "localhost:${pool_name}" blah-volume --project blah
  test_image_fingerprint="$(lxc_remote image list localhost: --format csv --columns f --project blah)"
  lxc_remote image delete "localhost:${test_image_fingerprint}" --project blah

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
  gen_cert_and_key "client-new"

  FINGERPRINT="$(lxc config trust list --format csv | cut -d, -f4)"

  # Try replacing the old certificate with a new one.
  # This should succeed as the user is listed as an admin.
  my_curl -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${LXD_CONF}/client-new.crt")\"}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}"

  # Record new fingerprint
  FINGERPRINT="$(lxc config trust list --format csv | cut -d, -f4)"

  # Move new certificate and key to LXD_CONF and back up old files.
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
  mv "${LXD_CONF}/client-new.crt" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client-new.key" "${LXD_CONF}/client.key"

  lxc_remote project create localhost:blah

  # Apply restrictions
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: false/restricted: true/" | lxc config trust edit "${FINGERPRINT}"

  # Add created project to the list of restricted projects. This way, the user will be listed as
  # a normal user instead of an admin.
  lxc config trust show "${FINGERPRINT}" | sed -e "s/projects: \[\]/projects: \[blah\]/" | lxc config trust edit "${FINGERPRINT}"

  # Try replacing the new certificate with the old one.
  # This should succeed as well as the certificate may be changed.
  my_curl -X PATCH -d "{\"certificate\":\"$(sed ':a;N;$!ba;s/\n/\\n/g' "${LXD_CONF}/client.crt.bak")\"}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}"

  # Move new certificate and key to LXD_CONF.
  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"

  # Record new fingerprint
  FINGERPRINT="$(lxc config trust list --format csv | cut -d, -f4)"

  # Trying to change other fields should fail as a non-admin.
  ! lxc_remote config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | lxc_remote config trust edit localhost:"${FINGERPRINT}" || false

  my_curl -X PATCH -d "{\"restricted\": false}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

  ! lxc_remote config trust show "${FINGERPRINT}" | sed -e "s/name:.*/name: bar/" | lxc_remote config trust edit localhost:"${FINGERPRINT}" || false

  my_curl -X PATCH -d "{\"name\": \"bar\"}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

  ! lxc_remote config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n- blah/projects: \[\]/' | lxc_remote config trust edit localhost:"${FINGERPRINT}" || false

  my_curl -X PATCH -d "{\"projects\": []}" "https://${LXD_ADDR}/1.0/certificates/${FINGERPRINT}" | grep -F '"error_code":403'

  # Cleanup
  lxc config trust show "${FINGERPRINT}" | sed -e "s/restricted: true/restricted: false/" | lxc config trust edit "${FINGERPRINT}"

  lxc config trust show "${FINGERPRINT}" | sed -e ':a;N;$!ba;s/projects:\n- blah/projects: \[\]/' | lxc config trust edit "${FINGERPRINT}"

  lxc project delete blah
}
