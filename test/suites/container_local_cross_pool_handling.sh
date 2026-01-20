test_container_local_cross_pool_handling() {
  local lxd_backend
  lxd_backend="$(storage_backend "$LXD_DIR")"

  local pool_opts=""
  if [ "${lxd_backend}" = "btrfs" ] || [ "${lxd_backend}" = "zfs" ]; then
    pool_opts="size=1GiB"
  elif [ "${lxd_backend}" = "ceph" ]; then
    pool_opts="volume.size=${DEFAULT_VOLUME_SIZE} ceph.osd.pg_num=16"
  elif [ "${lxd_backend}" = "lvm" ]; then
    pool_opts="volume.size=${DEFAULT_VOLUME_SIZE}"
  fi

  local otherPool
  otherPool="lxdtest-$(basename "${LXD_DIR}")-${lxd_backend}1"
  # shellcheck disable=SC2086,SC2248
  lxc storage create "${otherPool}" "${lxd_backend}" $pool_opts

  lxc init --empty c1
  lxc config show c1

  local originalPool
  originalPool="lxdtest-$(basename "${LXD_DIR}")"

  # Check volatile.apply_template is initialised during create.
  [ "$(lxc config get c1 volatile.apply_template)" = "create" ]
  lxc copy c1 c2 -s "${otherPool}"

  # Check volatile.apply_template is altered during copy.
  [ "$(lxc config get c2 volatile.apply_template)" = "copy" ]
  lxc storage volume show "${otherPool}" container/c2
  lxc delete c2
  lxc move c1 c2 -s "${otherPool}"

  # Check volatile.apply_template is not altered during move and rename.
  [ "$(lxc config get c2 volatile.apply_template)" = "create" ]
  ! lxc info c1 || false
  lxc storage volume show "${otherPool}" container/c2

  # Test moving back to original pool without renaming.
  lxc move c2 -s "${originalPool}"
  [ "$(lxc config get c2 volatile.apply_template)" = "create" ]
  lxc storage volume show "${originalPool}" container/c2
  lxc delete c2

  local data="${RANDOM}a"

  ensure_import_testimage
  lxc init testimage c1
  echo "${data}" | lxc file push -q - c1/root/canary
  lxc snapshot c1
  lxc copy c1 c2 -s "${otherPool}" --instance-only
  [ "$(lxc file pull -q c2/root/canary -)" = "${data}" ]
  lxc storage volume show "${otherPool}" container/c2
  ! lxc storage volume show "${otherPool}" container/c2/snap0 || false
  lxc delete c2
  lxc move c1 c2 -s "${otherPool}" --instance-only
  ! lxc info c1 || false
  [ "$(lxc file pull -q c2/root/canary -)" = "${data}" ]
  lxc storage volume show "${otherPool}" container/c2
  ! lxc storage volume show "${otherPool}" container/c2/snap0 || false
  lxc delete c2

  # Use different canary data
  data="${data}b"

  lxc init testimage c1
  echo "${data}" | lxc file push -q - c1/root/canary
  lxc snapshot c1
  lxc snapshot c1
  lxc copy c1 c2 -s "${otherPool}"
  [ "$(lxc file pull -q c2/root/canary -)" = "${data}" ]
  lxc storage volume show "${otherPool}" container/c2
  lxc storage volume show "${otherPool}" container/c2/snap0
  lxc storage volume show "${otherPool}" container/c2/snap1
  lxc delete c2
  lxc move c1 c2 -s "${otherPool}"
  ! lxc info c1 || false
  [ "$(lxc file pull -q c2/root/canary -)" = "${data}" ]
  lxc storage volume show "${otherPool}" container/c2
  lxc storage volume show "${otherPool}" container/c2/snap0
  lxc storage volume show "${otherPool}" container/c2/snap1
  lxc delete c2

  lxc storage delete "${otherPool}"
}

