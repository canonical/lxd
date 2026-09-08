test_image_auto_update() {
  if lxc image alias list testimage | grep -wF "testimage"; then
      lxc image delete testimage
  fi

  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(< "${LXD2_DIR}/lxd.addr")

  LXD_DIR=${LXD2_DIR} deps/import-busybox --alias testimage --public
  fp1="$(LXD_DIR=${LXD2_DIR} lxc image info testimage | awk '/^Fingerprint/ {print $2}')"

  # Create an image registry backed by a public cluster link to the second LXD.
  pending="$(lxc query --request POST /1.0/cluster/links --data "{\"name\":\"l2-link\",\"type\":\"public\",\"remote_address\":\"${LXD2_ADDR}\"}")"
  link_cert="$(echo "${pending}" | jq --exit-status '.certificate')"
  lxc query --request POST /1.0/cluster/links --data "{\"name\":\"l2-link\",\"type\":\"public\",\"remote_address\":\"${LXD2_ADDR}\",\"cluster_certificate\":${link_cert}}" > /dev/null
  lxc image registry create l2 cluster=l2-link source_project=default
  lxc init l2:testimage c1

  # Now the first image image is in the local store, since it was
  # downloaded to create c1.
  alias="$(lxc image info "${fp1}" | awk '{if ($1 == "Alias:") {print $2}}')"
  [ "${alias}" = "testimage" ]

  # Delete the first image from the remote store and replace it with a
  # new one with a different fingerprint (passing "--template create"
  # will do that).
  LXD_DIR=${LXD2_DIR} lxc image delete testimage
  LXD_DIR=${LXD2_DIR} deps/import-busybox --alias testimage --public --template create
  fp2="$(LXD_DIR=${LXD2_DIR} lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
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
        sleep 0.5
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
  alias="$(lxc image info "${fp2}" | awk '{if ($1 == "Alias:") {print $2}}')"
  [ "${alias}" = "testimage" ]

  lxc delete c1
  lxc image registry delete l2
  lxc cluster link delete l2-link
  lxc image delete "${fp2}"
  kill_lxd "$LXD2_DIR"
}
