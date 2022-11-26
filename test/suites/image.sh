test_image_expiry() {
  # shellcheck disable=2039,3043
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  # shellcheck disable=2153
  lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --password foo
  lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo

  # Create a container from a remote image
  lxc_remote init l1:testimage l2:c1
  fp=$(lxc_remote image info testimage | awk -F: '/^Fingerprint/ { print $2 }' | awk '{ print $1 }')

  # Confirm the image is cached
  [ -n "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-10)
  lxc_remote image list l2: | grep -q "${fpbrief}"

  # Test modification of image expiry date
  lxc_remote image info "l2:${fp}" | grep -q "Expires.*never"
  lxc_remote image show "l2:${fp}" | sed "s/expires_at.*/expires_at: 3000-01-01T00:00:00-00:00/" | lxc_remote image edit "l2:${fp}"
  lxc_remote image info "l2:${fp}" | grep -q "Expires.*3000"

  # Override the upload date
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}'" | grep -q "Rows affected: 1"

  # Trigger the expiry
  lxc_remote config set l2: images.remote_cache_expiry 1

  for _ in $(seq 20); do
    sleep 1
    ! lxc_remote image list l2: | grep -q "${fpbrief}" && break
  done

  ! lxc_remote image list l2: | grep -q "${fpbrief}" || false

  # Cleanup and reset
  lxc_remote delete l2:c1
  lxc_remote config set l2: images.remote_cache_expiry 10
  lxc_remote remote remove l1
  lxc_remote remote remove l2
  kill_lxd "$LXD2_DIR"
}

test_image_list_all_aliases() {
    ensure_import_testimage
    # shellcheck disable=2039,2034,2155,3043
    local sum="$(lxc image info testimage | grep ^Fingerprint | cut -d' ' -f2)"
    lxc image alias create zzz "$sum"
    lxc image list | grep -vq zzz
    # both aliases are listed if the "aliases" column is included in output
    lxc image list -c L | grep -q testimage
    lxc image list -c L | grep -q zzz

}

test_image_import_dir() {
    ensure_import_testimage
    lxc image export testimage
    # shellcheck disable=2039,2034,2155,3043
    local image="$(ls -1 -- *.tar.xz)"
    mkdir -p unpacked
    tar -C unpacked -xf "$image"
    # shellcheck disable=2039,2034,2155,3043
    local fingerprint="$(lxc image import unpacked | awk '{print $NF;}')"
    rm -rf "$image" unpacked

    lxc image export "$fingerprint"
    # shellcheck disable=2039,2034,2155,3043
    local exported="${fingerprint}.tar.xz"

    tar tvf "$exported" | grep -Fq metadata.yaml
    rm "$exported"
}

test_image_import_existing_alias() {
    ensure_import_testimage
    lxc init testimage c
    lxc publish c --alias newimage --alias image2
    lxc delete c
    lxc image export testimage testimage.file
    lxc image delete testimage
    # the image can be imported with an existing alias
    lxc image import testimage.file --alias newimage
    lxc image delete newimage image2
}

test_image_refresh() {
  # shellcheck disable=2039,3043
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo

  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | grep 'driver:' | awk '{print $2}')

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
  lxc publish l2:c1 l2: --alias testimage --public
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
