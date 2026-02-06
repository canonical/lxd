test_container_copy_incremental() {
  local lxd_backend
  lxd_backend="$(storage_backend "${LXD_DIR}")"

  ensure_import_testimage

  do_copy "" ""

  # cross-pool copy
  local other_backend="dir"
  if [ "${lxd_backend}" = "dir" ]; then
    other_backend="btrfs"
  fi
  local source_pool
  source_pool="lxdtest-$(basename "${LXD_DIR}")-${other_backend}-pool"
  lxc storage create "${source_pool}" "${other_backend}"

  do_copy "${source_pool}" "lxdtest-$(basename "${LXD_DIR}")"
  lxc storage rm "${source_pool}"
}

do_copy() {
  local source_pool="${1}"
  local target_pool="${2}"

  if [ -z "${source_pool}" ]; then
    source_pool="lxdtest-$(basename "${LXD_DIR}")"
  fi

  lxc init testimage c1 -s "${source_pool}"
  lxc storage volume set "${source_pool}" container/c1 user.foo=init

  # Set size to check this is supported during copy.
  lxc config device set c1 root size="${DEFAULT_VOLUME_SIZE}"

  targetPoolFlag=
  if [ -n "${target_pool}" ]; then
    targetPoolFlag="-s ${target_pool}"
  else
    target_pool="${source_pool}"
  fi

  # Initial copy
  # shellcheck disable=2086
  lxc copy c1 c2 ${targetPoolFlag}
  [ "$(lxc storage volume get "${target_pool}" container/c2 user.foo)" = "init" ]

  lxc start c1 c2

  # Target container may not be running when refreshing
  # shellcheck disable=2086
  ! lxc copy c1 c2 --refresh ${targetPoolFlag} || false

  # Create test file in c1
  lxc exec c1 -- touch /root/testfile1

  lxc stop -f c2

  # Refresh the container and validate the contents
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${targetPoolFlag}
  lxc start c2
  lxc exec c2 -- test -f /root/testfile1
  lxc stop -f c2

  # This will create snapshot c1/snap0
  lxc storage volume set "${source_pool}" container/c1 user.foo=snap0
  lxc snapshot c1
  lxc storage volume set "${source_pool}" container/c1 user.foo=snap1
  lxc snapshot c1
  lxc storage volume set "${source_pool}" container/c1 user.foo=main

  # Remove the testfile from c1 and refresh again
  lxc exec c1 -- rm /root/testfile1
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh --instance-only ${targetPoolFlag}
  lxc start c2
  ! lxc exec c2 -- test -f /root/testfile1 || false
  lxc stop -f c1 c2

  # Check whether snapshot c2/snap0 has been created
  ! lxc config show c2/snap0 || false
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${targetPoolFlag}
  lxc config show c2/snap0
  lxc config show c2/snap1
  [ "$(lxc storage volume get "${target_pool}" container/c2 user.foo)" = "init" ]
  [ "$(lxc storage volume get "${target_pool}" container/c2/snap0 user.foo)" = "snap0" ]
  [ "$(lxc storage volume get "${target_pool}" container/c2/snap1 user.foo)" = "snap1" ]

  # This will create snapshot c2/snap2
  lxc snapshot c2
  lxc config show c2/snap2
  lxc storage volume show "${target_pool}" container/c2/snap2

  # This should remove c2/snap2
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${targetPoolFlag}
  ! lxc config show c2/snap2 || false
  ! lxc storage volume show "${target_pool}" container/c2/snap2 || false

  lxc delete c1 c2
}

test_container_copy_start() {
  local lxd_backend
  lxd_backend="$(storage_backend "${LXD_DIR}")"

  ensure_import_testimage
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"

  echo "==> Check that a copied container by default is stopped"
  lxc copy c1 c2
  [ "$(lxc list -f csv -c s c2)" = "STOPPED" ]

  lxc delete -f c2

  echo "==> Check that a container can be copied and started with the same command"
  lxc copy c1 c2 --start

  echo "==> Check the copied container is running"
  [ "$(lxc list -f csv -c s c2)" = "RUNNING" ]

  lxc stop -f c2

  echo "==> Check --refresh cannot be used together with --start"
  ! lxc copy c1 c2 --refresh --start || false

  lxc delete -f c2
  lxc snapshot c1

  echo "==> Check that a container snapshot can be copied and started with the same command"
  lxc copy c1/snap0 c2 --start

  echo "==> Check the copied container is running"
  [ "$(lxc list -f csv -c s c2)" = "RUNNING" ]

  lxc delete -f c2

  poolDriver="$(storage_backend "${LXD_DIR}")"

  # Spawn an additional cluster to allow copy through migration.
  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node.
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  # Set up a TLS identity with admin permissions.
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group create copy
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group permission add copy server admin

  # Create a token on the LXD cluster.
  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc auth identity create tls/copy --group=copy --quiet)"

  # Add the LXD cluster as a remote.
  lxc remote add cls 100.64.1.101:8443 --token="${token}"

  # Create a temp network for binding the local LXD to allow pull mode migration.
  lxc network create foo
  new_address="$(lxc network get foo ipv4.address | cut -d/ -f1)"
  old_address="$(lxc config get core.https_address)"
  lxc config set core.https_address "${new_address}:8443"

  for mode in pull push relay; do
    echo "==> Check that a container can be copied to a remote and started with the same command (${mode} mode)"
    lxc copy c1 cls:c2 --mode "${mode}" --start

    echo "==> Check the copied container is running"
    [ "$(lxc list -f csv -c s cls:c2)" = "RUNNING" ]

    lxc delete -f cls:c2

    echo "==> Check that a container snapshot can be copied to a remote and started with the same command (${mode} mode)"
    lxc copy c1/snap0 cls:c2 --mode "${mode}" --start

    echo "==> Check the copied container is running"
    [ "$(lxc list -f csv -c s cls:c2)" = "RUNNING" ]

    lxc delete -f cls:c2
  done

  # Ensure we can do cross-pool copy.
  local other_backend="dir"
  if [ "${lxd_backend}" = "dir" ]; then
    other_backend="btrfs"
  fi
  local new_pool
  new_pool="lxdtest-$(basename "${LXD_DIR}")-${other_backend}-pool"
  lxc storage create "${new_pool}" "${other_backend}"

  echo "==> Check that a container which is copied between pools can be started with the same command"
  lxc copy c1 c2 --storage "${new_pool}" --start

  echo "==> Check the copied container is running"
  [ "$(lxc list -f csv -c s c2)" = "RUNNING" ]

  lxc delete -f c2

  echo "==> Check that a container snapshot which is copied between pools can be started with the same command"
  lxc copy c1/snap0 c2 --storage "${new_pool}" --start

  echo "==> Check the copied container is running"
  [ "$(lxc list -f csv -c s c2)" = "RUNNING" ]

  lxc delete -f c2

  # Use the cluster to check start after copy on the same pool but between different members.
  lxc copy c1 cls:c1 --target node1
  lxc snapshot cls:c1

  # When the server decides to use copy instead of migration, the mode is ignored.
  # This is the case when using a remote pool.
  for mode in pull push relay; do
    echo "==> Check that a container can be copied to a different member and started with the same command (${mode} mode)"
    LXD_DIR="${LXD_ONE_DIR}" lxc copy c1 c2 --target node2 --mode "${mode}" --start

    echo "==> Check the copied container is running"
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c s c2)" = "RUNNING" ]

    LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c2

    echo "==> Check that a container snapshot can be copied to a different member and started with the same command (${mode} mode)"
    LXD_DIR="${LXD_ONE_DIR}" lxc copy c1/snap0 c2 --target node2 --mode "${mode}" --start

    echo "==> Check the copied container is running"
    [ "$(LXD_DIR="${LXD_ONE_DIR}" lxc list -f csv -c s c2)" = "RUNNING" ]

    LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c2
  done

  # Cleanup
  lxc delete -f c1
  lxc delete -f cls:c1
  lxc remote remove cls
  lxc config set core.https_address "${old_address}"
  lxc network delete foo
  lxc storage delete "${new_pool}"

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}
