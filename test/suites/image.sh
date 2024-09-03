test_image_expiry() {
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  token="$(lxc config trust add --name foo -q)"
  # shellcheck disable=2153
  lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --token "${token}"

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --token "${token}"

  # Create containers from a remote image in two projects.
  lxc_remote project create l2:p1 -c features.images=true -c features.profiles=false
  lxc_remote init l1:testimage l2:c1 --project default
  lxc_remote project switch l2:p1
  lxc_remote init l1:testimage l2:c2
  lxc_remote project switch l2:default

  fp="$(lxc_remote image info testimage | awk '/^Fingerprint/ {print $2}')"

  # Confirm the image is cached
  [ -n "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-12)
  lxc_remote image list l2: | grep -q "${fpbrief}"

  # Test modification of image expiry date
  lxc_remote image info "l2:${fp}" | grep -q "Expires.*never"
  lxc_remote image show "l2:${fp}" | sed "s/expires_at.*/expires_at: 3000-01-01T00:00:00-00:00/" | lxc_remote image edit "l2:${fp}"
  lxc_remote image info "l2:${fp}" | grep -q "Expires.*3000"

  # Override the upload date for the image record in the default project.
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id = 1" | grep -q "Rows affected: 1"

  # Trigger the expiry
  lxc_remote config set l2: images.remote_cache_expiry 1

  for _ in $(seq 20); do
    sleep 1
    ! lxc_remote image list l2: | grep -q "${fpbrief}" && break
  done

  ! lxc_remote image list l2: | grep -q "${fpbrief}" || false

  # Check image is still in p1 project and has not been expired.
  lxc_remote image list l2: --project p1 | grep -q "${fpbrief}"

  # Test instance can still be created in p1 project.
  lxc_remote project switch l2:p1
  lxc_remote init l1:testimage l2:c3
  lxc_remote project switch l2:default

  # Override the upload date for the image record in the p1 project.
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id > 1" | grep -q "Rows affected: 1"
  lxc_remote project set l2:p1 images.remote_cache_expiry=1

  # Trigger the expiry in p1 project by changing global images.remote_cache_expiry.
  lxc_remote config unset l2: images.remote_cache_expiry

  for _ in $(seq 20); do
    sleep 1
    ! lxc_remote image list l2: --project p1 | grep -q "${fpbrief}" && break
  done

  ! lxc_remote image list l2: --project p1 | grep -q "${fpbrief}" || false

  # Cleanup and reset
  lxc_remote delete -f l2:c1
  lxc_remote delete -f l2:c2 --project p1
  lxc_remote delete -f l2:c3 --project p1
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
    lxc image list | grep -vq zzz
    # both aliases are listed if the "aliases" column is included in output
    lxc image list -c L | grep -q testimage
    lxc image list -c L | grep -q zzz

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
    local exported
    exported="${fingerprint}.tar.xz"

    tar tvf "$exported" metadata.yaml
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
    rm testimage.file.tar.xz
    lxc image delete newimage image2
}

test_image_refresh() {
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --token "${token}"

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
    lxd sql global 'select name from storage_volumes' | grep "${fp}"
    ! lxd sql global 'select name from storage_volumes' | grep "${new_fp}" || false
  fi

  # Refresh image
  lxc image refresh testimage

  # Ensure the old image is gone.
  ! lxc image info "${fp}" || false

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record has been replaced with new one.
    ! lxd sql global 'select name from storage_volumes' | grep "${fp}" || false
    lxd sql global 'select name from storage_volumes' | grep "${new_fp}"
  fi

  # Cleanup
  lxc rm l2:c1
  lxc rm c1
  lxc remote rm l2
  kill_lxd "${LXD2_DIR}"
}
