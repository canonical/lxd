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
  lxc image export localhost:testimage "${TEST_DIR}/" --project default
  [ "${test_image_fingerprint}" = "$(sha256sum "${TEST_DIR}/${test_image_fingerprint}.tar.xz" | cut -d' ' -f1)" ]

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

  # Delete resources in project blah so that we can modify project features.
  lxc_remote delete localhost:blah-instance --project blah
  lxc_remote storage volume delete "localhost:${pool_name}" blah-volume --project blah
  lxc_remote image delete "localhost:${test_image_fingerprint}" --project blah

  # Ensure we can create and view resources that are not enabled for the project (e.g. their effective project is
  # the default project).

  ### IMAGES (initial value is true for new projects)

  # Unset the images feature (the default is false).
  lxc project unset blah features.images

  # The test image in the default project should be visible via project blah.
  lxc_remote image info "localhost:${test_image_fingerprint}" --project blah
  lxc_remote image show "localhost:${test_image_fingerprint}" --project blah
  test_image_fingerprint_short="$(echo "${test_image_fingerprint}" | cut -c1-12)"
  lxc_remote image list localhost: --project blah | grep -F "${test_image_fingerprint_short}"

  # The restricted client can't view it via project default.
  ! lxc_remote image info "localhost:${test_image_fingerprint}" --project default || false
  ! lxc_remote image show "localhost:${test_image_fingerprint}" --project default || false
  ! lxc_remote image list localhost: --project default | grep -F "${test_image_fingerprint_short}" || false

  # The restricted client can edit the image.
  lxc_remote image set-property "localhost:${test_image_fingerprint}" requirements.secureboot true --project blah
  lxc_remote image unset-property "localhost:${test_image_fingerprint}" requirements.secureboot --project blah

  # The restricted client can delete the image.
  lxc_remote image delete "localhost:${test_image_fingerprint}" --project blah

  # The restricted client can create images.
  lxc_remote image import "${TEST_DIR}/${test_image_fingerprint}.tar.xz" localhost: --project blah

  # Clean up
  lxc_remote image delete "localhost:${test_image_fingerprint}" --project blah


  ### NETWORKS (initial value is false in new projects).

  # Create a network in the default project.
  networkName="net$$"
  lxc network create "${networkName}" --project default

  # The network we created in the default project is visible in project blah.
  lxc_remote network show "localhost:${networkName}" --project blah
  lxc_remote network list localhost: --project blah | grep -F "${networkName}"

  # The restricted client can't view it via project default.
  ! lxc_remote network show "localhost:${networkName}" --project default || false
  ! lxc_remote network list localhost: --project default | grep -F "${networkName}" || false

  # The restricted client can edit the network.
  lxc_remote network set "localhost:${networkName}" user.foo=bar --project blah

  # The restricted client can delete the network.
  lxc_remote network delete "localhost:${networkName}" --project blah

  # Create a network in the blah project.
  lxc_remote network create localhost:blah-network --project blah

  # Network is visible to restricted client in project blah.
  lxc_remote network show localhost:blah-network --project blah
  lxc_remote network list localhost: --project blah | grep blah-network

  # The network is actually in the default project.
  lxc network show blah-network --project default

  # The restricted client can't view it via the default project.
  ! lxc_remote network show localhost:blah-network --project default || false

  # The restricted client can delete the network.
  lxc_remote network delete localhost:blah-network --project blah


  ### NETWORK ZONES (initial value is false in new projects).

  # Create a network zone in the default project.
  zoneName="zone$$"
  lxc network zone create "${zoneName}" --project default

  # The network zone we created in the default project is visible in project blah.
  lxc_remote network zone show "localhost:${zoneName}" --project blah
  lxc_remote network zone list localhost: --project blah | grep -F "${zoneName}"

  # The restricted client can't view it via project default.
  ! lxc_remote network zone show "localhost:${zoneName}" --project default || false
  ! lxc_remote network zone list localhost: --project default | grep -F "${zoneName}" || false

  # The restricted client can edit the network zone.
  lxc_remote network zone set "localhost:${zoneName}" user.foo=bar --project blah

  # The restricted client can delete the network zone.
  lxc_remote network zone delete "localhost:${zoneName}" --project blah

  # Create a network zone in the blah project.
  lxc_remote network zone create localhost:blah-zone --project blah

  # Network zone is visible to restricted client in project blah.
  lxc_remote network zone show localhost:blah-zone --project blah
  lxc_remote network zone list localhost: --project blah | grep blah-zone

  # The network zone is actually in the default project.
  lxc network zone show blah-zone --project default

  # The restricted client can't view it via the default project.
  ! lxc_remote network zone show localhost:blah-zone --project default || false

  # The restricted client can delete the network zone.
  lxc_remote network zone delete localhost:blah-zone --project blah


  ### PROFILES (initial value is true for new projects)

  # Unset the profiles feature (the default is false).
  lxc project unset blah features.profiles

  # Create a profile in the default project.
  profileName="prof$$"
  lxc profile create "${profileName}" --project default

  # The profile we created in the default project is visible in project blah.
  lxc_remote profile show "localhost:${profileName}" --project blah
  lxc_remote profile list localhost: --project blah | grep -F "${profileName}"

  # The restricted client can't view it via project default.
  ! lxc_remote profile show "localhost:${profileName}" --project default || false
  ! lxc_remote profile list localhost: --project default | grep -F "${profileName}" || false

  # The restricted client can edit the profile.
  lxc_remote profile set "localhost:${profileName}" user.foo=bar --project blah

  # The restricted client can delete the profile.
  lxc_remote profile delete "localhost:${profileName}" --project blah

  # Create a profile in the blah project.
  lxc_remote profile create localhost:blah-profile --project blah

  # Profile is visible to restricted client in project blah.
  lxc_remote profile show localhost:blah-profile --project blah
  lxc_remote profile list localhost: --project blah | grep blah-profile

  # The profile is actually in the default project.
  lxc profile show blah-profile --project default

  # The restricted client can't view it via the default project.
  ! lxc_remote profile show localhost:blah-profile --project default || false

  # The restricted client can delete the profile.
  lxc_remote profile delete localhost:blah-profile --project blah


  ### STORAGE VOLUMES (initial value is true for new projects)

  # Unset the storage volumes feature (the default is false).
  lxc project unset blah features.storage.volumes

  # Create a storage volume in the default project.
  volName="vol$$"
  lxc storage volume create "${pool_name}" "${volName}" --project default

  # The storage volume we created in the default project is visible in project blah.
  lxc_remote storage volume show "localhost:${pool_name}" "${volName}" --project blah
  lxc_remote storage volume list "localhost:${pool_name}" --project blah | grep -F "${volName}"

  # The restricted client can't view it via project default.
  ! lxc_remote storage volume show "localhost:${pool_name}" "${volName}" --project default || false
  ! lxc_remote storage volume list "localhost:${pool_name}" --project default | grep -F "${volName}" || false

  # The restricted client can edit the storage volume.
  lxc_remote storage volume set "localhost:${pool_name}" "${volName}" user.foo=bar --project blah

  # The restricted client can delete the storage volume.
  lxc_remote storage volume delete "localhost:${pool_name}" "${volName}" --project blah

  # Create a storage volume in the blah project.
  lxc_remote storage volume create "localhost:${pool_name}" blah-volume --project blah

  # Storage volume is visible to restricted client in project blah.
  lxc_remote storage volume show "localhost:${pool_name}" blah-volume --project blah
  lxc_remote storage volume list "localhost:${pool_name}" --project blah | grep blah-volume

  # The storage volume is actually in the default project.
  lxc storage volume show "${pool_name}" blah-volume --project default

  # The restricted client can't view it via the default project.
  ! lxc_remote storage volume show "localhost:${pool_name}" blah-volume --project default || false

  # The restricted client can delete the storage volume.
  lxc_remote storage volume delete "localhost:${pool_name}" blah-volume --project blah

  ### STORAGE BUCKETS (initial value is true for new projects)
  create_object_storage_pool s3

  # Unset the storage buckets feature (the default is false).
  lxc project unset blah features.storage.buckets

  # Create a storage bucket in the default project.
  bucketName="bucket$$"
  lxc storage bucket create s3 "${bucketName}" --project default

  # The storage bucket we created in the default project is visible in project blah.
  lxc_remote storage bucket show localhost:s3 "${bucketName}" --project blah
  lxc_remote storage bucket list localhost:s3 --project blah | grep -F "${bucketName}"

  # The restricted client can't view it via project default.
  ! lxc_remote storage bucket show localhost:s3 "${bucketName}" --project default || false
  ! lxc_remote storage bucket list localhost:s3 --project default | grep -F "${bucketName}" || false

  # The restricted client can edit the storage bucket.
  lxc_remote storage bucket set localhost:s3 "${bucketName}" user.foo=bar --project blah

  # The restricted client can delete the storage bucket.
  lxc_remote storage bucket delete localhost:s3 "${bucketName}" --project blah

  # Create a storage bucket in the blah project.
  lxc_remote storage bucket create localhost:s3 blah-bucket --project blah

  # Storage bucket is visible to restricted client in project blah.
  lxc_remote storage bucket show localhost:s3 blah-bucket --project blah
  lxc_remote storage bucket list localhost:s3 --project blah | grep blah-bucket

  # The storage bucket is actually in the default project.
  lxc storage bucket show s3 blah-bucket --project default

  # The restricted client can't view it via the default project.
  ! lxc_remote storage bucket show localhost:s3 blah-bucket --project default || false

  # The restricted client can delete the storage bucket.
  lxc_remote storage bucket delete localhost:s3 blah-bucket --project blah

  # Cleanup
  delete_object_storage_pool s3
  rm "${TEST_DIR}/${test_image_fingerprint}.tar.xz"
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

test_tls_version() {
  echo "TLS 1.3 just works"
  my_curl -X GET "https://${LXD_ADDR}"
  my_curl --tlsv1.3 -X GET "https://${LXD_ADDR}"

  echo "TLS 1.3 with various ciphersuites"
  for cipher in TLS_AES_256_GCM_SHA384 TLS_CHACHA20_POLY1305_SHA256 TLS_AES_128_GCM_SHA256; do
    echo "Testing TLS 1.3: ${cipher}"
    my_curl --tlsv1.3 --tls13-ciphers "${cipher}" -X GET "https://${LXD_ADDR}"
  done

  echo "TLS 1.2 is refused with a protocol version error"
  ! my_curl --tls-max 1.2 -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" || false
  my_curl --tls-max 1.2 -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" | grep -F "alert protocol version"

  echo "Enable TLS 1.2 with LXD_INSECURE_TLS=true"
  shutdown_lxd "${LXD_DIR}"
  export LXD_INSECURE_TLS=true
  respawn_lxd "${LXD_DIR}" true

  echo "TLS 1.3 is still working and used by default"
  my_curl -X GET "https://${LXD_ADDR}"
  my_curl --tlsv1.3 -X GET "https://${LXD_ADDR}"

  echo "TLS 1.2 is now working"
  my_curl --tls-max 1.2 -X GET "https://${LXD_ADDR}"

  echo "TLS 1.2 with ciphers known to work"
  for cipher in ECDHE-ECDSA-AES128-GCM-SHA256 ECDHE-ECDSA-AES256-GCM-SHA384 ECDHE-ECDSA-CHACHA20-POLY1305; do
    echo "Testing TLS 1.2: ${cipher}"
    my_curl --tls-max 1.2 --ciphers "${cipher}" -X GET "https://${LXD_ADDR}"
  done

  echo "TLS 1.2 does not work with RSA auth when the server uses ECDSA cert"
  for cipher in ECDHE-RSA-AES128-GCM-SHA256 ECDHE-RSA-AES256-GCM-SHA384; do
    echo "Testing TLS 1.2: ${cipher}"
    ! my_curl --tls-max 1.2 --ciphers "${cipher}" -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" || false
    my_curl --tls-max 1.2 --ciphers "${cipher}" -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" | grep -F "alert handshake failure"
  done

  echo "TLS 1.2 with ciphers known to be refused with a handshake failure"
  for cipher in ECDHE-ECDSA-AES128-SHA256 ECDHE-ECDSA-AES256-SHA384; do
    echo "Testing TLS 1.2: ${cipher}"
    ! my_curl --tls-max 1.2 --ciphers "${cipher}" -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" || false
    my_curl --tls-max 1.2 --ciphers "${cipher}" -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" | grep -F "alert handshake failure"
  done

  echo "TLS 1.2 with ciphers known to be a broken pipe error"
  for cipher in ECDHE-ECDSA-AES128-SHA ECDHE-ECDSA-AES256-SHA; do
    echo "Testing TLS 1.2: ${cipher}"
    ! my_curl --tls-max 1.2 --ciphers "${cipher}" -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" || false
    my_curl --tls-max 1.2 --ciphers "${cipher}" -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" | grep -F "Broken pipe, errno"
  done

  echo "TLS 1.1 is not working"
  ! my_curl --tls-max 1.1 -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" || false
  my_curl --tls-max 1.1 -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" | grep -F "no protocols available"

  echo "Disable TLS 1.2"
  shutdown_lxd "${LXD_DIR}"
  unset LXD_INSECURE_TLS
  respawn_lxd "${LXD_DIR}" true

  echo "TLS 1.2 is refused with a protocol version error"
  ! my_curl --tls-max 1.2 -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" || false
  my_curl --tls-max 1.2 -X GET "https://${LXD_ADDR}" -w "%{errormsg}\n" | grep -F "alert protocol version"
}
