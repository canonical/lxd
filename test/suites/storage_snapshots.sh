test_storage_volume_snapshots() {
  ensure_import_testimage

  # shellcheck disable=2039,3043
  local LXD_STORAGE_DIR lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false

  # shellcheck disable=2039,3043
  local storage_pool storage_volume
  storage_pool="lxdtest-$(basename "${LXD_STORAGE_DIR}")-pool"
  storage_volume="${storage_pool}-vol"

  lxc storage create "$storage_pool" "$lxd_backend"
  lxc storage volume create "${storage_pool}" "${storage_volume}"
  lxc launch testimage c1 -s "${storage_pool}"
  lxc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt
  # Create file on volume
  echo foobar > "${TEST_DIR}/testfile"
  lxc file push "${TEST_DIR}/testfile" c1/mnt/testfile

  # Validate file
  lxc exec c1 -- test -f /mnt/testfile
  [ "$(lxc exec c1 -- cat /mnt/testfile)" = 'foobar' ]

  lxc storage volume detach "${storage_pool}" "${storage_volume}" c1
  # This will create a snapshot named 'snap0'
  lxc storage volume snapshot "${storage_pool}" "${storage_volume}"
  lxc storage volume list "${storage_pool}" |  grep "${storage_volume}/snap0"
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'name: snap0'
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 0001-01-01T00:00:00Z'

  # edit volume snapshot description
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | sed 's/^description:.*/description: foo/' | lxc storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep -q 'description: foo'

  # edit volume snapshot expiry date
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | sed 's/^expires_at:.*/expires_at: 2100-01-02T15:04:05Z/' | lxc storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  # Depending on the timezone of the runner, some values will be different.
  # Both the year (2100) and the month (01) will be constant though.
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep -q '^expires_at: 2100-01'
  # Reset/remove expiry date
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | sed '/^expires_at:/d' | lxc storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep -q '^expires_at: 0001-01-01T00:00:00Z'

  lxc storage volume set "${storage_pool}" "${storage_volume}" snapshots.expiry '1d'
  lxc storage volume snapshot "${storage_pool}" "${storage_volume}"
  ! lxc storage volume show "${storage_pool}" "${storage_volume}/snap1" | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  lxc storage volume snapshot "${storage_pool}" "${storage_volume}" --no-expiry
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap2" | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  lxc storage volume rm "${storage_pool}" "${storage_volume}/snap2"
  lxc storage volume rm "${storage_pool}" "${storage_volume}/snap1"

  # Test snapshot renaming
  lxc storage volume snapshot "${storage_pool}" "${storage_volume}"
  lxc storage volume list "${storage_pool}" |  grep "${storage_volume}/snap1"
  lxc storage volume show "${storage_pool}" "${storage_volume}/snap1" | grep 'name: snap1'
  lxc storage volume rename "${storage_pool}" "${storage_volume}/snap1" "${storage_volume}/foo"
  lxc storage volume list "${storage_pool}" |  grep "${storage_volume}/foo"
  lxc storage volume show "${storage_pool}" "${storage_volume}/foo" | grep 'name: foo'

  lxc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt
  # Delete file on volume
  lxc file delete c1/mnt/testfile

  # Validate file
  ! lxc exec c1 -- test -f /mnt/testfile || false

  # This should fail since you cannot restore a snapshot when the target volume
  # is attached to the container
  ! lxc storage volume restore "${storage_pool}" "${storage_volume}" snap0 || false

  lxc stop -f c1
  lxc storage volume restore "${storage_pool}" "${storage_volume}" foo

  lxc start c1
  lxc storage volume detach "${storage_pool}" "${storage_volume}" c1
  lxc storage volume restore "${storage_pool}" "${storage_volume}" foo
  lxc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt

  # Validate file
  lxc exec c1 -- test -f /mnt/testfile
  [ "$(lxc exec c1 -- cat /mnt/testfile)" = 'foobar' ]

  lxc storage volume detach "${storage_pool}" "${storage_volume}" c1
  lxc delete -f c1
  lxc storage volume delete "${storage_pool}" "${storage_volume}"

  # Check snapshots naming conflicts.
  lxc storage volume create "${storage_pool}" "vol1"
  lxc storage volume create "${storage_pool}" "vol1-snap0"
  lxc storage volume snapshot "${storage_pool}" "vol1" "snap0"
  lxc storage volume delete "${storage_pool}" "vol1"
  lxc storage volume delete "${storage_pool}" "vol1-snap0"


  lxc storage delete "${storage_pool}"

  # shellcheck disable=SC2031,2269
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
