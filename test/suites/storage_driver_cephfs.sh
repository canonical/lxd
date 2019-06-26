test_storage_driver_cephfs() {
  # shellcheck disable=2039
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")
  if [ "$lxd_backend" != "ceph" ] || [ -z "${LXD_CEPH_CEPHFS:-}" ]; then
    return
  fi

  # Simple create/delete attempt
  lxc storage create cephfs cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")"
  lxc storage delete cephfs

  # Second create (confirm got cleaned up properly)
  lxc storage create cephfs cephfs source="${LXD_CEPH_CEPHFS}/$(basename "${LXD_DIR}")"
  lxc storage info cephfs

  # Creation, rename and deletion
  lxc storage volume create cephfs vol1
  if [ "$(uname -r | cut -d. -f1)" -gt 4 ]; then
    lxc storage volume set cephfs vol1 size 100MB
  fi
  lxc storage volume rename cephfs vol1 vol2
  lxc storage volume copy cephfs/vol2 cephfs/vol1
  lxc storage volume delete cephfs vol1
  lxc storage volume delete cephfs vol2

  # Snapshots
  lxc storage volume create cephfs vol1
  lxc storage volume snapshot cephfs vol1
  lxc storage volume snapshot cephfs vol1
  lxc storage volume snapshot cephfs vol1 blah1
  lxc storage volume rename cephfs vol1/blah1 vol1/blah2
  lxc storage volume snapshot cephfs vol1 blah1
  lxc storage volume delete cephfs vol1/snap0
  lxc storage volume delete cephfs vol1/snap1
  lxc storage volume restore cephfs vol1 blah1
  lxc storage volume copy cephfs/vol1 cephfs/vol2 --volume-only
  lxc storage volume copy cephfs/vol1 cephfs/vol3 --volume-only
  lxc storage volume delete cephfs vol1
  lxc storage volume delete cephfs vol2
  lxc storage volume delete cephfs vol3

  # Cleanup
  lxc storage delete cephfs
}
