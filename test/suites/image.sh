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
  lxc_remote init l1:testimage l2:c1
  fp=$(lxc_remote image info testimage | awk -F: '/^Fingerprint/ { print $2 }' | awk '{ print $1 }')
  [ ! -z "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-10)

  lxc_remote image list l2: | grep -q "${fpbrief}"

  lxc_remote remote set-default l2
  lxc_remote config set images.remote_cache_expiry 0
  lxc_remote remote set-default local

  ! lxc_remote image list l2: | grep -q "${fpbrief}"

  lxc_remote delete l2:c1

  # reset the default expiry
  lxc_remote remote set-default l2
  lxc_remote config set images.remote_cache_expiry 10
  lxc_remote remote set-default local

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
