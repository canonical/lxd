test_storage_driver_cephfs() {
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" != "ceph" ] || [ -z "${LXD_CEPH_CEPHFS:-}" ]; then
    return
  fi

  # Simple create/delete attempt
  lxc storage create cephfs cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")"
  lxc storage delete cephfs

  # Test invalid key combinations for auto-creation of cephfs entities.
  ! lxc storage create cephfs cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.osd_pg_num=32 || true
  ! lxc storage create cephfs cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.meta_pool=xyz || true
  ! lxc storage create cephfs cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.data_pool=xyz || true
  ! lxc storage create cephfs cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")" cephfs.create_missing=true cephfs.data_pool=xyz_data cephfs.meta_pool=xyz_meta || true


  # Test cephfs storage volumes.
  for fs in "cephfs" "cephfs2" ; do
    if [ "${fs}" = "cephfs" ]; then
      # Create one cephfs with pre-existing OSDs.
      lxc storage create "${fs}" cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")"
    else
      # Create one cephfs by creating the OSDs and the cephfs itself.
      lxc storage create "${fs}" cephfs source=cephfs2 cephfs.create_missing=true cephfs.data_pool=xyz_data cephfs.meta_pool=xyz_meta
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
