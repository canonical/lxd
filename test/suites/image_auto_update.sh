test_image_auto_update() {
  if lxc image alias list | grep -q "^| testimage\\s*|.*$"; then
      lxc image delete testimage
  fi

  # shellcheck disable=2039
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  (LXD_DIR=${LXD2_DIR} deps/import-busybox --alias testimage --public)
  fp1=$(LXD_DIR=${LXD2_DIR} lxc image info testimage | awk -F: '/^Fingerprint/ { print $2 }' | awk '{ print $1 }')

  lxc remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo
  lxc init l2:testimage c1

  # Now the first image image is in the local store, since it was
  # downloaded to create c1.
  alias=$(lxc image info "${fp1}" | awk -F: '/^    Alias/ { print $2 }' | awk '{ print $1 }')
  [ "${alias}" = "testimage" ]

  # Delete the first image from the remote store and replace it with a
  # new one with a different fingerprint (passing "--template create"
  # will do that).
  (LXD_DIR=${LXD2_DIR} lxc image delete testimage)
  (LXD_DIR=${LXD2_DIR} deps/import-busybox --alias testimage --public --template create)
  fp2=$(LXD_DIR=${LXD2_DIR} lxc image info testimage | awk -F: '/^Fingerprint/ { print $2 }' | awk '{ print $1 }')
  [ "${fp1}" != "${fp2}" ]

  # Restart the server to force an image refresh immediately
  # shellcheck disable=2153
  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  # Check that the first image got deleted from the local storage
  #
  # XXX: Since the auto-update logic runs asynchronously we need to wait
  #      a little bit before it actually completes.
  retries=600
  while [ "${retries}" != "0" ]; do
    if lxc image info "${fp1}" > /dev/null 2>&1; then
        sleep 2
        retries=$((retries-1))
        continue
    fi
    break
  done

  if [ "${retries}" -eq 0 ]; then
      echo "First image ${fp1} not deleted from local store"
      return 1
  fi

  # The second image replaced the first one in the local storage.
  alias=$(lxc image info "${fp2}" | awk -F: '/^    Alias/ { print $2 }' | awk '{ print $1 }')
  [ "${alias}" = "testimage" ]

  lxc delete c1
  lxc remote remove l2
  lxc image delete "${fp2}"
  kill_lxd "$LXD2_DIR"
}
