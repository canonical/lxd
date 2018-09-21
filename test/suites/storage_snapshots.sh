test_storage_volume_snapshots() {
  ensure_import_testimage

  # shellcheck disable=2039
  local LXD_STORAGE_DIR lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  spawn_lxd "${LXD_STORAGE_DIR}" false

  # edit storage and pool description
  # shellcheck disable=2039
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

  lxc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt
  # Delete file on volume
  lxc file delete c1/mnt/testfile

  # Validate file
  ! lxc exec c1 -- test -f /mnt/testfile

  # This should fail since you cannot restore a snapshot when the target volume
  # is attached to the container
  ! lxc storage volume restore "${storage_pool}" "${storage_volume}" snap0

  lxc stop c1
  lxc storage volume restore "${storage_pool}" "${storage_volume}" snap0

  lxc start c1
  lxc storage volume detach "${storage_pool}" "${storage_volume}" c1
  lxc storage volume restore "${storage_pool}" "${storage_volume}" snap0
  lxc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt

  # Validate file
  lxc exec c1 -- test -f /mnt/testfile
  [ "$(lxc exec c1 -- cat /mnt/testfile)" = 'foobar' ]

  lxc storage volume detach "${storage_pool}" "${storage_volume}" c1
  lxc delete -f c1
  lxc storage volume delete "${storage_pool}" "${storage_volume}"
  lxc storage delete "${storage_pool}"

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}
