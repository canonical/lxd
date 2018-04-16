test_image_expiry() {
  # shellcheck disable=2039
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  if ! lxc_remote remote list | grep -q l1; then
    # shellcheck disable=2153
    lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --password foo
  fi

  if ! lxc_remote remote list | grep -q l2; then
    lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo
  fi

  # Create a container from a remote image
  lxc_remote init l1:testimage l2:c1
  fp=$(lxc_remote image info testimage | awk -F: '/^Fingerprint/ { print $2 }' | awk '{ print $1 }')

  # Confirm the image is cached
  [ ! -z "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-10)
  lxc_remote image list l2: | grep -q "${fpbrief}"

  # Override the upload date
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}'" | grep -q "Rows affected: 1"

  # Trigger the expiry
  lxc_remote config set l2: images.remote_cache_expiry 1

  # shellcheck disable=SC2034
  for i in $(seq 20); do
    sleep 1
    ! lxc_remote image list l2: | grep -q "${fpbrief}" && break
  done

  ! lxc_remote image list l2: | grep -q "${fpbrief}" || false

  # Cleanup and reset
  lxc_remote delete l2:c1
  lxc_remote config set l2: images.remote_cache_expiry 10
  lxc_remote remote remove l2
  kill_lxd "$LXD2_DIR"
}

test_image_list_all_aliases() {
    ensure_import_testimage
    # shellcheck disable=2039,2034,2155
    local sum=$(lxc image info testimage | grep ^Fingerprint | cut -d' ' -f2)
    lxc image alias create zzz "$sum"
    lxc image list | grep -vq zzz
    # both aliases are listed if the "aliases" column is included in output
    lxc image list -c L | grep -q testimage
    lxc image list -c L | grep -q zzz

}

test_image_import_dir() {
    ensure_import_testimage
    lxc image export testimage
    # shellcheck disable=2039,2034,2155
    local image=$(ls -1 -- *.tar.xz)
    mkdir -p unpacked
    tar -C unpacked -xf "$image"
    # shellcheck disable=2039,2034,2155
    local fingerprint=$(lxc image import unpacked | awk '{print $NF;}')
    rm -rf "$image" unpacked

    lxc image export "$fingerprint"
    # shellcheck disable=2039,2034,2155
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
