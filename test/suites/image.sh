test_image_expiry() {
  local LXD2_DIR LXD2_ADDR
  # shellcheck disable=2153
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(< "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  local token
  token="$(lxc config trust add --name foo -q)"
  # shellcheck disable=2153
  lxc_remote remote add l1 "${LXD_ADDR}" --token "${token}"

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add l2 "${LXD2_ADDR}" --token "${token}"

  echo "Create containers from a remote image in three projects."
  lxc_remote project create l2:p1 -c features.images=true -c features.profiles=false
  lxc_remote init l1:testimage l2:c1 --project default
  lxc_remote project switch l2:p1
  lxc_remote init l1:testimage l2:c2
  lxc_remote project create l2:p2 -c features.images=true -c features.profiles=false
  lxc_remote project switch l2:p2

  local fp
  fp="$(lxc_remote image info testimage | awk '/^Fingerprint/ {print $2}')"

  echo "Create instance from cached image."
  lxc_remote init l1:testimage l2:c3
  lxc_remote delete -f l2:c3

  echo "Confirm the image is cached."
  [ -n "${fp}" ]
  local fpbrief
  fpbrief=$(echo "${fp}" | cut -c 1-12)
  lxc_remote image list -f csv -c f l2: | grep -xF "${fpbrief}"

  echo "Project can still be deleted since cached images are pruned."
  lxc_remote project delete l2:p2

  echo "Switch back to default project and confirm image is still cached."
  lxc_remote project switch l2:default
  lxc_remote image list -f csv -c f l2: | grep -xF "${fpbrief}"

  echo "Test modification of image expiry date."
  lxc_remote image info "l2:${fp}" | grep "Expires.*never"
  lxc_remote image show "l2:${fp}" | sed "s/expires_at.*/expires_at: 3000-01-01T00:00:00-00:00/" | lxc_remote image edit "l2:${fp}"
  lxc_remote image info "l2:${fp}" | grep "Expires.*3000"

  echo "Override the upload date for the image record in the default project."
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id = 1" | grep -xF "Rows affected: 1"

  echo "Trigger the expiry."
  lxc_remote config set l2: images.remote_cache_expiry 1

  for _ in $(seq 40); do
    if ! lxc_remote image info l2:"${fpbrief}" 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  ! lxc_remote image info l2:"${fpbrief}" || false

  echo "Check image is still in p1 project and has not been expired."
  lxc_remote image list -f csv -c f l2: --project p1 | grep -xF "${fpbrief}"

  echo "Test instance can still be created in p1 project."
  lxc_remote project switch l2:p1
  lxc_remote init l1:testimage l2:c3
  lxc_remote project switch l2:default

  echo "Override the upload date for the image record in the p1 project."
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id > 1" | grep -xF "Rows affected: 1"
  lxc_remote project set l2:p1 images.remote_cache_expiry=1

  echo "Trigger the expiry in p1 project by changing global images.remote_cache_expiry."
  lxc_remote config unset l2: images.remote_cache_expiry

  for _ in $(seq 40); do
    if ! lxc_remote image info l2:"${fpbrief}" --project p1 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  ! lxc_remote image info l2:"${fpbrief}" --project p1 || false

  echo "==> Clean up instances."
  lxc_remote delete -f l2:c1
  lxc_remote delete -f l2:c2 --project p1
  lxc_remote delete -f l2:c3 --project p1

  echo "==> Copy remote image to a local store in project p1. It should be marked non-cached."
  lxc_remote image copy l1:testimage l2: --target-project p1

  echo "==> Check that the locally saved image in project p1 is marked as non-cached."
  lxc_remote image info l2:"${fp}" --project p1 | grep -xF "Cached: no"

  echo "==> Create a new instance in the default project with a remote image. It should be cached."
  lxc_remote init l1:testimage l2:c1

  echo "==> Check that the locally saved image in default project is marked as cached."
  lxc_remote image info l2:"${fp}" | grep -xF "Cached: yes"

  echo "==> Check that the image file exists locally."
  ls -l "${LXD2_DIR}/images/${fp}"

  echo "==> Update last_use_date for both images to Jan 1, 2000."
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date = '2000-01-01T00:00:00Z'" | grep -xF "Rows affected: 2"

  echo "==> Trigger the expiry. Image in the default project should get pruned, but image in project p1 should remain."
  lxc_remote config set l2: images.remote_cache_expiry 1

  for _ in $(seq 40); do
    if ! lxc_remote image info l2:"${fpbrief}" 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  echo "==> Check that image in the default project was deleted."
  ! lxc_remote image info l2:"${fpbrief}" || false

  echo "==> Check that image in project p1 still exists."
  lxc_remote image info l2:"${fpbrief}" --project p1

  echo "==> Check that the image file still exists locally because it is used by image in project p1."
  ls -l "${LXD2_DIR}/images/${fp}"

  echo "==> Create an instance in project p1 using locally saved image."
  lxc_remote init l2:"${fpbrief}" l2:c2 --project p1

  echo "==> Cleanup and reset."
  lxc_remote delete -f l2:c1
  lxc_remote delete -f l2:c2 --project p1
  lxc_remote image delete l2:"${fpbrief}" --project p1
  lxc_remote project delete l2:p1
  lxc_remote remote remove l1
  lxc_remote remote remove l2
  kill_lxd "$LXD2_DIR"
}

test_image_list_all_aliases() {
    ensure_import_testimage
    local sum
    sum="$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
    lxc image alias create zzz "$sum"
    # both aliases are listed if the "aliases" column is included in output
    local expected_output
    expected_output="$(echo -e '"testimage\nzzz"')"
    [ "$(lxc image list -f csv -c L)" = "${expected_output}" ]
    lxc image alias delete zzz
}

test_image_list_remotes() {
    if [ -n "${LXD_OFFLINE:-}" ]; then
        export TEST_UNMET_REQUIREMENT="LXD_OFFLINE mode enabled, skipping"
        return 0
    fi

    # list images from the `images:` and `ubuntu-minimal:` builtin remotes if they are reachable
    lxc remote list -f csv | while IFS=, read -r name url _; do
        if [ "${name}" != "images" ] && [ "${name}" != "ubuntu-minimal" ]; then
            continue
        fi

        # Check if there is connectivity
        curl --head --silent "${url}" > /dev/null || continue

        lxc image list "${name}:" > /dev/null
    done
}

test_image_import_dir() {
    ensure_import_testimage
    lxc image export testimage
    local image
    image="$(ls -1 -- *.tar.xz)"
    mkdir -p unpacked
    tar -C unpacked -xf "$image"
    local fingerprint
    fingerprint="$(lxc image import unpacked | awk '{print $NF;}')"
    rm -rf "$image" unpacked

    lxc image export "$fingerprint"
    lxc image delete "${fingerprint}"
    local exported
    exported="${fingerprint}.tar.xz"

    tar tvf "$exported" --occurrence=1 metadata.yaml
    rm "$exported"
}

test_image_import_existing_alias() {
    ensure_import_testimage
    lxc init testimage c
    lxc publish c --alias newimage --alias image2
    lxc delete c
    lxc image export testimage testimage.file
    lxc image delete testimage

    # XXX: ensure_import_testimage imports a `.tar.xz` image which is why once exported, those extensions are appended
    # the image can be imported with an existing alias
    lxc image import testimage.file.tar.xz --alias newimage

    # Test for proper error message when importing an image to a non-existing project
    output="$(! lxc image import testimage.file.tar.xz --project nonexistingproject 2>&1 || false)"
    echo "${output}" | grep -F "Project not found"

    rm testimage.file.tar.xz
    lxc image delete newimage image2
}

test_image_refresh() {
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(< "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add l2 "${LXD2_ADDR}" --token "${token}"

  poolDriver="$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')"

  # Publish image
  lxc image copy testimage l2: --alias testimage --public
  fp="$(lxc image info l2:testimage | awk '/Fingerprint: / {print $2}')"
  lxc image rm testimage

  # Create container from published image
  lxc init l2:testimage c1

  # Create an alias for the received image
  lxc image alias create testimage "${fp}"

  # Change image and publish it
  lxc init l2:testimage l2:c1
  echo test | lxc file push - l2:c1/tmp/testfile
  lxc publish l2:c1 l2: --alias testimage --reuse --public
  new_fp="$(lxc image info l2:testimage | awk '/Fingerprint: / {print $2}')"

  # Ensure the images differ
  [ "${fp}" != "${new_fp}" ]

  # Check original image exists before refresh.
  lxc image info "${fp}"

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record exists and new one doesn't.
    lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${fp}"
    ! lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${new_fp}" || false
  fi

  # Refresh image
  lxc image refresh testimage

  # Ensure the old image is gone.
  ! lxc image info "${fp}" || false

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record has been replaced with new one.
    ! lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${fp}" || false
    lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${new_fp}"
  fi

  # Cleanup
  lxc rm l2:c1
  lxc rm c1
  lxc remote rm l2
  kill_lxd "${LXD2_DIR}"
}

test_images_public() {
  echo "==> Check public image handling for untrusted callers"
  run_images_public

  echo "==> Check public image handling for restricted TLS clients with no permissions"
  token="$(lxc config trust add --name tmp --restricted --quiet)"

  # Generate certificates.
  tmpdir="$(mktemp -d -p "${TEST_DIR}" XXX)"
  LXD_CONF="${tmpdir}" gen_cert_and_key client

  # Gain trust.
  LXD_CONF="${tmpdir}" lxc remote add tmp "${token}"

  # Rename certificates, this tells "run_images_public" which assertions to run.
  mv "${tmpdir}/client.crt" "${tmpdir}/restricted.crt"
  mv "${tmpdir}/client.key" "${tmpdir}/restricted.key"

  # Run test.
  CERT_DIR="${tmpdir}" CERT_NAME="restricted" run_images_public

  # Clean up.
  lxc config trust remove "$(cert_fingerprint "${tmpdir}/restricted.crt")"
  rm -rf "${tmpdir}"

  echo "==> Check public image handling for fine-grained TLS clients with no groups"
  token="$(lxc auth identity create tls/tmp --quiet)"

  # Generate certificates.
  tmpdir="$(mktemp -d -p "${TEST_DIR}" XXX)"
  LXD_CONF="${tmpdir}" gen_cert_and_key client

  # Gain trust.
  LXD_CONF="${tmpdir}" lxc remote add tmp "${token}"

  # Rename certificates, this tells "run_images_public" which assertions to run.
  mv "${tmpdir}/client.crt" "${tmpdir}/fine-grained.crt"
  mv "${tmpdir}/client.key" "${tmpdir}/fine-grained.key"

  # Run test.
  CERT_DIR="${tmpdir}" CERT_NAME="fine-grained" run_images_public

  # Clean up.
  lxc auth identity delete tls/tmp
  rm -rf "${tmpdir}"
}

run_images_public() {
  query() {
    # Function to use for querying. (Cant use my_curl for untrusted requests).
    # SC2068 is disabled because we do want arguments to be re-split.
    url="${1}"
    shift
    if [ -n "${CERT_DIR:-}" ]; then
      # shellcheck disable=2153,2068
      curl -s --cacert "${LXD_DIR}/server.crt" --cert "${CERT_DIR}/${CERT_NAME}.crt" --key "${CERT_DIR}/${CERT_NAME}.key" "https://${LXD_ADDR}${url}" ${@}
    else
      # shellcheck disable=2153,2068
      curl -s --cacert "${LXD_DIR}/server.crt" "https://${LXD_ADDR}${url}" ${@}
    fi
  }

  # Add a private image to the default project.
  deps/import-busybox --alias default-img

  # Create a project and import an image.
  lxc project create foo
  deps/import-busybox --project foo --alias foo-img

  # All callers see an empty list of images in the default project.
  query /1.0/images | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  query /1.0/images?project=default | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  query /1.0/images?recursion=1 | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  query /1.0/images?recursion=1\&project=default | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'

  if [ -z "${CERT_NAME:-}" ]; then
    # Untrusted callers see a generic 404 for non-default projects, or a 403 if using "all-projects".
    query /1.0/images?project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?all-projects=true | jq --exit-status '.error == "Untrusted callers may only access public images in the default project" and .error_code == 403'
    query /1.0/images?recursion=1\&all-projects=true | jq --exit-status '.error == "Untrusted callers may only access public images in the default project" and .error_code == 403'
  elif [ "${CERT_NAME}" = "restricted" ]; then
    # Restricted TLS clients without access to any projects see a 403 for non-default projects, and a 403 if using all-projects.
    query /1.0/images?project=foo | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?recursion=1\&project=foo  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?project=bar | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?recursion=1\&project=bar  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?all-projects=true  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?recursion=1\&all-projects=true  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
  elif [ "${CERT_NAME}" = "fine-grained" ]; then
    # Fine-grained identities with no permissions see a generic 404 for non-default projects, and a 200 with an empty list if using "all-projects".
    query /1.0/images?project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?all-projects=true | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
    query /1.0/images?recursion=1\&all-projects=true | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  else
      echo "Unexpected certificate name: ${CERT_NAME}"
      false
  fi

  # All users see a not found error for the aliases.
  query /1.0/images/aliases/default-img | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/aliases/foo-img?project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/aliases/foo-img?project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # Get the image fingerprint.
  fingerprint="$(lxc query /1.0/images/aliases/foo-img?project=foo | jq --exit-status --raw-output '.target')"

  # All users see a not found error when getting or exporting the image.
  query "/1.0/images/${fingerprint}?project=foo" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query "/1.0/images/${fingerprint}/export?project=foo" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query "/1.0/images/${fingerprint}?project=bar" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query "/1.0/images/${fingerprint}/export?project=bar" | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # No callers can use an invalid secret.
  query /1.0/images/"${fingerprint}"?project=foo\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?project=foo\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"?project=bar\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?project=bar\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # Get a secret for "default-img" (in the default project).
  secret="$(lxc -X POST query "/1.0/images/${fingerprint}/secret" | jq --exit-status --raw-output '.metadata.secret')"

  # All callers can view the image with a valid secret.
  query /1.0/images/"${fingerprint}"?secret="${secret}" | jq --exit-status '.status_code == 200'

  # All callers can export the image with a valid secret.
  query /1.0/images/"${fingerprint}"/export?secret="${secret}" -o "${TEST_DIR}/private.img"
  rm "${TEST_DIR}/private.img"

  # Get a secret for "foo-img" (in the foo project).
  secret="$(lxc -X POST query "/1.0/images/${fingerprint}/secret?project=foo" | jq --exit-status --raw-output '.metadata.secret')"

  # All callers can view the image with a valid secret.
  query /1.0/images/"${fingerprint}"?project=foo\&secret="${secret}" | jq --exit-status '.status_code == 200'

  # All callers can export the image with a valid secret.
  query /1.0/images/"${fingerprint}"/export?project=foo\&secret="${secret}" -o "${TEST_DIR}/private.img"
  rm "${TEST_DIR}/private.img"

  # The secrets do not work 5 seconds after being used.
  sleep 5
  query /1.0/images/"${fingerprint}"?secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"?project=foo\&secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?project=foo\&secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # Set the image in the default project to public.
  lxc image show "${fingerprint}" | sed -e "s/public: false/public: true/" | lxc image edit "${fingerprint}"

  # All callers can see the public image when listing.
  query /1.0/images | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'
  query /1.0/images?project=default | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'
  query /1.0/images?recursion=1 | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'
  query /1.0/images?recursion=1\&project=default | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'

  # All callers can view aliases of public images in the default project.
  query /1.0/images/aliases/default-img | jq --exit-status '.status_code == 200'
  query /1.0/images/aliases/default-img?project=default | jq --exit-status '.status_code == 200'

  # All callers can get the image with a prefix of 12 characters or more.
  query "/1.0/images/%25" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/${fingerprint:0:11}" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/%25${fingerprint:0:11}" | jq --exit-status '.error == "Image fingerprint prefix must contain only lowercase hexadecimal characters" and .error_code == 400'
  query "/1.0/images/${fingerprint}abc" | jq --exit-status '.error == "Image fingerprint cannot be longer than 64 characters" and .error_code == 400'
  query "/1.0/images/${fingerprint:0:12}" | jq --exit-status '.status_code == 200'
  query "/1.0/images/${fingerprint}" | jq --exit-status '.status_code == 200'
  query "/1.0/images/${fingerprint}?project=default" | jq --exit-status '.status_code == 200'

  # All callers can export the public image if using a valid prefix.
  query "/1.0/images/%25/export" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/${fingerprint:0:11}/export" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/%25${fingerprint:0:11}/export" | jq --exit-status '.error == "Image fingerprint prefix must contain only lowercase hexadecimal characters" and .error_code == 400'
  query "/1.0/images/${fingerprint}abc/export" | jq --exit-status '.error == "Image fingerprint cannot be longer than 64 characters" and .error_code == 400'
  query "/1.0/images/${fingerprint}/export" -o "${TEST_DIR}/public1.img"
  query "/1.0/images/${fingerprint}/export?project=default" -o "${TEST_DIR}/public2.img"
  query "/1.0/images/${fingerprint:0:12}/export?project=default" -o "${TEST_DIR}/public3.img"
  rm "${TEST_DIR}"/public{1,2,3}.img

  # Clean up.
  lxc image delete "${fingerprint}" --project foo
  lxc project delete foo
  lxc image delete "${fingerprint}"
}
