test_storage_driver_pure() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" != "pure" ]; then
    export TEST_UNMET_REQUIREMENT="pure specific test, not for ${lxd_backend}"
    return
  fi

  local LXD_STORAGE_DIR

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  spawn_lxd "${LXD_STORAGE_DIR}" false

  (
    set -eux
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # Create 2 storage pools.
    poolName1="lxdtest-$(basename "${LXD_DIR}")-pool1"
    poolName2="lxdtest-$(basename "${LXD_DIR}")-pool2"
    configure_pure_pool "${poolName1}"
    configure_pure_pool "${poolName2}"

    # Configure default volume size for pools.
    lxc storage set "${poolName1}" volume.size=25MiB
    lxc storage set "${poolName2}" volume.size=25MiB

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="${poolName1}"

    # Import image into default storage pool.
    ensure_import_testimage

    # Muck around with some containers on various pools.
    lxc init testimage c1pool1 -s "${poolName1}"
    lxc list -c b c1pool1 | grep "${poolName1}"

    lxc init testimage c2pool2 -s "${poolName2}"
    lxc list -c b c2pool2 | grep "${poolName2}"

    lxc launch images:alpine/edge c3pool1 -s "${poolName1}"
    lxc list -c b c3pool1 | grep "${poolName1}"

    lxc launch images:alpine/edge c4pool2 -s "${poolName2}"
    lxc list -c b c4pool2 | grep "${poolName2}"

    lxc storage set "${poolName1}" volume.block.filesystem xfs
    lxc storage set "${poolName1}" volume.size 300MiB # modern xfs requires 300MiB or more
    lxc init testimage c5pool1 -s "${poolName1}"

    # Test whether dependency tracking is working correctly. We should be able
    # to create a container, copy it, which leads to a dependency relation
    # between the source container's storage volume and the copied container's
    # storage volume. Now, we delete the source container which will trigger a
    # rename operation and not an actual delete operation. Now we create a
    # container of the same name as the source container again, create a copy of
    # it to introduce another dependency relation. Now we delete the source
    # container again. This should work. If it doesn't it means the rename
    # operation tries to map the two source to the same name.
    lxc init testimage a -s "${poolName1}"
    lxc copy a b
    lxc delete a
    lxc init testimage a -s "${poolName1}"
    lxc copy a c
    lxc delete a
    lxc delete b
    lxc delete c

    lxc storage volume create "${poolName1}" c1pool1
    lxc storage volume attach "${poolName1}" c1pool1 c1pool1 testDevice /opt
    ! lxc storage volume attach "${poolName1}" c1pool1 c1pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c1pool1 c1pool1
    lxc storage volume attach "${poolName1}" custom/c1pool1 c1pool1 testDevice /opt
    ! lxc storage volume attach "${poolName1}" custom/c1pool1 c1pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c1pool1 c1pool1

    lxc storage volume create "${poolName1}" c2pool2
    lxc storage volume attach "${poolName1}" c2pool2 c2pool2 testDevice /opt
    ! lxc storage volume attach "${poolName1}" c2pool2 c2pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c2pool2 c2pool2
    lxc storage volume attach "${poolName1}" custom/c2pool2 c2pool2 testDevice /opt
    ! lxc storage volume attach "${poolName1}" custom/c2pool2 c2pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName1}" c2pool2 c2pool2

    lxc storage volume create "${poolName2}" c3pool1
    lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice /opt
    ! lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c3pool1 c3pool1
    lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice /opt
    ! lxc storage volume attach "${poolName2}" c3pool1 c3pool1 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c3pool1 c3pool1

    lxc storage volume create "${poolName2}" c4pool2
    lxc storage volume attach "${poolName2}" c4pool2 c4pool2 testDevice /opt
    ! lxc storage volume attach "${poolName2}" c4pool2 c4pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c4pool2 c4pool2
    lxc storage volume attach "${poolName2}" custom/c4pool2 c4pool2 testDevice /opt
    ! lxc storage volume attach "${poolName2}" custom/c4pool2 c4pool2 testDevice2 /opt || false
    lxc storage volume detach "${poolName2}" c4pool2 c4pool2
    lxc storage volume rename "${poolName2}" c4pool2 c4pool2-renamed
    lxc storage volume rename "${poolName2}" c4pool2-renamed c4pool2

    lxc delete -f c1pool1
    lxc delete -f c3pool1
    lxc delete -f c5pool1

    lxc delete -f c4pool2
    lxc delete -f c2pool2

    lxc storage volume set "${poolName1}" c1pool1 size 500MiB
    lxc storage volume unset "${poolName1}" c1pool1 size

    lxc storage volume delete "${poolName1}" c1pool1
    lxc storage volume delete "${poolName1}" c2pool2
    lxc storage volume delete "${poolName2}" c3pool1
    lxc storage volume delete "${poolName2}" c4pool2

    do_storage_driver_pure_image_recovery "${poolName1}"

    lxc image delete testimage
    lxc profile device remove default root
    lxc storage delete "${poolName1}"
    lxc storage delete "${poolName2}"
  )

  # shellcheck disable=SC2031
  kill_lxd "${LXD_STORAGE_DIR}"
}

do_storage_driver_pure_image_recovery() {
  local pool="${1}"
  local fingerprint vol_uuid vol_name token snap_ref count

  local gateway="${PURE_GATEWAY%/}"
  local tls_opts=()
  if [ "${PURE_GATEWAY_VERIFY:-true}" = "false" ]; then
    tls_opts=("-k")
  fi

  sub_test "Verify a corrupted image volume is automatically regenerated before instance creation."

  ensure_import_testimage
  fingerprint=$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')

  # Pure Storage uses UUID-based volume names with a type prefix (i=image) and an optional
  # content-type suffix (b=block). Derive the volume name from the image volume's volatile.uuid.
  vol_uuid=$(lxc storage volume get "${pool}" "image/${fingerprint}" volatile.uuid)
  vol_name="i-$(echo "${vol_uuid}" | tr -d '-')"

  # Authenticate once for all API calls in this sub-test.
  token=$(curl -s "${tls_opts[@]}" -X POST \
    -H "Content-Type: application/json" \
    -d "{\"api-token\": \"${PURE_API_TOKEN}\"}" \
    -D - -o /dev/null \
    "${gateway}/api/2.21/login" | grep -i "^x-auth-token:" | tr -d '\r' | awk '{print $2}')
  [ -n "${token}" ]

  # Simulate an aborted image download by destroying the readonly snapshot.
  # Pure Storage requires a two-step soft/hard delete: destroy first, then eradicate.
  snap_ref="${pool}::${vol_name}.readonly"
  curl --fail-with-body -s "${tls_opts[@]}" -X PATCH \
    -H "X-Auth-Token: ${token}" \
    -H "Content-Type: application/json" \
    -d '{"destroyed": true}' \
    "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}"
  curl --fail-with-body -s "${tls_opts[@]}" -X DELETE \
    -H "X-Auth-Token: ${token}" \
    "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}"

  # Verify the snapshot was actually deleted before proceeding.
  count=$(curl -s "${tls_opts[@]}" -X GET \
    -H "X-Auth-Token: ${token}" \
    "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}" | jq '.total_item_count')
  [ "${count}" -eq 0 ]

  # The image integrity check detects the missing readonly snapshot and
  # regenerates the image volume before the instance is created.
  lxc init testimage c-recovery

  # Verify the readonly snapshot was recreated as part of the regeneration.
  count=$(curl -s "${tls_opts[@]}" -X GET \
    -H "X-Auth-Token: ${token}" \
    "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}" | jq '.total_item_count')
  [ "${count}" -gt 0 ]

  lxc delete c-recovery

  # Test VM image recovery (if VM tests are enabled).
  if [ "${LXD_VM_TESTS:-}" = "true" ]; then
    sub_test "Verify a corrupted VM image volume is automatically regenerated before instance creation."

    ensure_import_ubuntu_vm_image
    fingerprint=$(lxc image info ubuntu-vm | awk '/^Fingerprint/ {print $2}')

    # Pure Storage VM images consist of a block volume (i-<uuid>-b) and a companion FS volume
    # (i-<uuid>). Both share the same volatile.uuid, so one lookup suffices.
    vol_uuid=$(lxc storage volume get "${pool}" "image/${fingerprint}" volatile.uuid)
    local block_vol_name fs_vol_name
    block_vol_name="i-$(echo "${vol_uuid}" | tr -d '-')-b"
    fs_vol_name="i-$(echo "${vol_uuid}" | tr -d '-')"

    # Re-authenticate for the VM sub-test.
    token=$(curl -s "${tls_opts[@]}" -X POST \
      -H "Content-Type: application/json" \
      -d "{\"api-token\": \"${PURE_API_TOKEN}\"}" \
      -D - -o /dev/null \
      "${gateway}/api/2.21/login" | grep -i "^x-auth-token:" | tr -d '\r' | awk '{print $2}')
    [ -n "${token}" ]

    # Simulate an aborted VM image download by removing the block volume's readonly snapshot.
    # ValidateImageVolume checks this snapshot first, so removing it alone is sufficient to
    # trigger recovery without needing to also remove the companion FS snapshot.
    snap_ref="${pool}::${block_vol_name}.readonly"
    curl --fail-with-body -s "${tls_opts[@]}" -X PATCH \
      -H "X-Auth-Token: ${token}" \
      -H "Content-Type: application/json" \
      -d '{"destroyed": true}' \
      "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}"
    curl --fail-with-body -s "${tls_opts[@]}" -X DELETE \
      -H "X-Auth-Token: ${token}" \
      "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}"

    # Verify the snapshot was actually deleted before proceeding.
    count=$(curl -s "${tls_opts[@]}" -X GET \
      -H "X-Auth-Token: ${token}" \
      "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}" | jq '.total_item_count')
    [ "${count}" -eq 0 ]

    # The image integrity check should detect the missing readonly snapshot
    # and regenerate the complete VM image structure before instance creation.
    lxc init ubuntu-vm vm-recovery --vm

    # Verify both readonly snapshots were recreated as part of the regeneration.
    count=$(curl -s "${tls_opts[@]}" -X GET \
      -H "X-Auth-Token: ${token}" \
      "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}" | jq '.total_item_count')
    [ "${count}" -gt 0 ]

    snap_ref="${pool}::${fs_vol_name}.readonly"
    count=$(curl -s "${tls_opts[@]}" -X GET \
      -H "X-Auth-Token: ${token}" \
      "${gateway}/api/2.21/volume-snapshots?names=${snap_ref}" | jq '.total_item_count')
    [ "${count}" -gt 0 ]

    lxc delete vm-recovery
    lxc image delete ubuntu-vm
  fi
}
