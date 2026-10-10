test_storage_driver_cephfs() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" != "ceph" ]; then
    export TEST_UNMET_REQUIREMENT="ceph specific test, not for ${lxd_backend}"
    return
  fi

  if [ -z "${LXD_CEPH_CEPHFS:-}" ]; then
    export TEST_UNMET_REQUIREMENT="required 'LXD_CEPH_CEPHFS' not set"
    return
  fi

  # Simple create/delete attempt
  lxc storage create cephfs cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")"
  lxc storage delete cephfs

  # Test invalid key combinations for auto-creation of cephfs entities.
  ! lxc storage create cephfs cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.osd_pg_num=32 || false
  ! lxc storage create cephfs cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.meta_pool=xyz || false
  ! lxc storage create cephfs cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.data_pool=xyz || false
  ! lxc storage create cephfs cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.create_missing=true cephfs.data_pool=xyz_data cephfs.meta_pool=xyz_meta || false
  ! lxc storage create cephfs cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" volume.security.shared=true || false

  # Test cephfs storage volumes.
  for fs in "cephfs" "cephfs2" ; do
    if [ "${fs}" = "cephfs" ]; then
      # Create one cephfs with pre-existing OSDs.
      lxc storage create "${fs}" cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")"
    else
      # Create one cephfs by creating the OSDs and the cephfs itself.
      lxc storage create "${fs}" cephfs cephfs.path=cephfs2 cephfs.create_missing=true cephfs.data_pool=xyz_data cephfs.meta_pool=xyz_meta
    fi

    # Confirm got cleaned up properly
    lxc storage info "${fs}"

    # Creation, rename and deletion
    lxc storage volume create "${fs}" vol1
    lxc storage volume set "${fs}" vol1 size 100MiB
    lxc storage volume rename "${fs}" vol1 vol2
    lxc storage volume copy "${fs}"/vol2 "${fs}"/vol1
    lxc storage volume delete "${fs}" vol1
    lxc storage volume delete "${fs}" vol2

    # Snapshots
    lxc storage volume create "${fs}" vol1
    lxc storage volume snapshot "${fs}" vol1
    lxc storage volume snapshot "${fs}" vol1
    lxc storage volume snapshot "${fs}" vol1 blah1
    lxc storage volume rename "${fs}" vol1/blah1 vol1/blah2
    lxc storage volume snapshot "${fs}" vol1 blah1
    lxc storage volume delete "${fs}" vol1/snap0
    lxc storage volume delete "${fs}" vol1/snap1
    lxc storage volume restore "${fs}" vol1 blah1
    lxc storage volume copy "${fs}"/vol1 "${fs}"/vol2 --volume-only
    lxc storage volume copy "${fs}"/vol1 "${fs}"/vol3 --volume-only
    lxc storage volume delete "${fs}" vol1
    lxc storage volume delete "${fs}" vol2
    lxc storage volume delete "${fs}" vol3

    # Cleanup
    lxc storage delete "${fs}"

    # Remove the filesystem so we can create a new one.
    ceph fs fail "${fs}"
    ceph fs rm "${fs}" --yes-i-really-mean-it
  done

  # Recreate the fs for other tests.
  ceph fs new cephfs cephfs_meta cephfs_data --force
}

# test_clustering_storage_cephfs attaches a cephfs custom volume to a container on each member of a two member
# cluster. It changes the volume through each member and reads the backup files of both containers.
test_clustering_storage_cephfs() {
  local poolDriver cert backup_c1 backup_c2
  poolDriver="$(storage_backend "${LXD_INITIAL_DIR}")"
  if [ "${poolDriver}" != "ceph" ]; then
    export TEST_UNMET_REQUIREMENT="ceph specific test, not for ${poolDriver}"
    return
  fi

  if [ -z "${LXD_CEPH_CEPHFS:-}" ]; then
    export TEST_UNMET_REQUIREMENT="required 'LXD_CEPH_CEPHFS' not set"
    return
  fi

  spawn_lxd_and_bootstrap_cluster "${poolDriver}"
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" lxc storage create cephfs cephfs --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create cephfs cephfs --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create cephfs cephfs cephfs.path="${LXD_CEPH_CEPHFS}/$(basename "${LXD_ONE_DIR}")"

  # The root volumes of the containers are on a local pool, which only the member of each container can access.
  # A dir pool also keeps the backup files readable from the test without mounting the volumes.
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create local dir --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create local dir --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage create local dir

  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c1 --target node1 -s local
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c2 --target node2 -s local
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create cephfs vol1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume attach cephfs vol1 c1 /mnt
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume attach cephfs vol1 c2 /mnt
  backup_c1="${LXD_ONE_DIR}/containers/c1/backup.yaml"
  backup_c2="${LXD_TWO_DIR}/containers/c2/backup.yaml"
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == ""' < "${backup_c1}"
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == ""' < "${backup_c2}"

  sub_test "Volume changes through a member update the backup file of the container on that member only"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume set cephfs vol1 size=256MiB
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | .config.size == "256MiB"' < "${backup_c1}"
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | .config.size != "256MiB"' < "${backup_c2}"

  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume set cephfs vol1 size=512MiB
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | .config.size == "256MiB"' < "${backup_c1}"
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | .config.size == "512MiB"' < "${backup_c2}"

  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume snapshot cephfs vol1 snap0
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == "snap0"' < "${backup_c1}"
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == ""' < "${backup_c2}"

  LXD_DIR="${LXD_TWO_DIR}" lxc storage volume rename cephfs vol1/snap0 vol1/snap1
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == "snap0"' < "${backup_c1}"
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == "snap1"' < "${backup_c2}"

  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete cephfs vol1/snap1
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == ""' < "${backup_c1}"
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == "snap1"' < "${backup_c2}"

  # The member of the container writes the backup file again when it updates the container.
  LXD_DIR="${LXD_ONE_DIR}" lxc config set c2 user.foo=bar
  yq --exit-status '.volumes.[] | select(.name == "vol1" and .pool == "cephfs") | (.snapshots // []) | map(.name) | join(",") == ""' < "${backup_c2}"

  # Cleanup.
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete cephfs vol1
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete testimage
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete cephfs
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete local

  # The root disk of the default profile uses the pool, and a pool in use cannot be deleted.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}
