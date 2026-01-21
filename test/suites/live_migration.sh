#!/bin/bash

# test_clustering_live_migration spawns a 2-node LXD cluster, creates a virtual machine on the first node
# and live migrates it to the second node. If the storage backend is remote, it also creates a custom
# storage volume which needs to be live migrated as along the virtual machine.
test_clustering_live_migration() {
  poolDriver="$(storage_backend "${LXD_INITIAL_DIR}")"
  if [ "${poolDriver}" = "lvm" ]; then
    # TODO: LVM live migration runs into:
    # Error: Failed migration on source: Failed transferring migration storage snapshot: Specified block job not found
    export TEST_UNMET_REQUIREMENT="Storage driver ${poolDriver} is currently unsupported"
    return 0
  fi

  # For remote storage drivers, we perform the live migration with custom storage pool attached as well.
  isRemoteDriver=false
  if [ "${poolDriver}" == "ceph" ]; then
    isRemoteDriver=true

    # Set test live migration env var to prevent LXD erroring out during unmount of the
    # source instance volume during live migration on the same host. During unmount the
    # volume is already mounted to the destination instance and will error with "device
    # or resource busy" error.
    export LXD_TEST_LIVE_MIGRATION_ON_THE_SAME_HOST=true
  fi

  # Spawn the first node and bootstrap the cluster.
  spawn_lxd_and_bootstrap_cluster "${poolDriver}"

  local cert
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"

  # Spawn a second node.
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  # Set up a TLS identity with admin permissions.
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group create live-migration
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group permission add live-migration server admin

  token="$(LXD_DIR="${LXD_ONE_DIR}" lxc auth identity create tls/live-migration --group=live-migration --quiet)"
  LXD_DIR="${LXD_ONE_DIR}" lxc remote add cls 100.64.1.101:8443 --token="${token}"

  LXD_DIR="${LXD_ONE_DIR}" ensure_import_ubuntu_vm_image

  # Storage pool created when spawning LXD cluster is "data".
  poolName="data"
  LXD_DIR="${LXD_ONE_DIR}" lxc storage set "${poolName}" volume.size="${SMALLEST_VM_ROOT_DISK}"

  # Initialize the VM.
  LXD_DIR="${LXD_ONE_DIR}" lxc init ubuntu-vm vm \
    --vm \
    --config limits.cpu=2 \
    --config limits.memory=768MiB \
    --config migration.stateful=true \
    --device root,size="${SMALLEST_VM_ROOT_DISK}" \
    --target node1

  # For remote storage drivers, test live migration with custom volume as well.
  if [ "${isRemoteDriver}" = true ]; then
    # Attach the block volume to the VM.
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create "${poolName}" vmdata --type=block size=1MiB
    LXD_DIR="${LXD_ONE_DIR}" lxc config device add vm vmdata disk pool="${poolName}" source=vmdata
  fi

  # Start the VM.
  LXD_DIR="${LXD_ONE_DIR}" lxc start vm
  LXD_DIR="${LXD_ONE_DIR}" waitInstanceReady vm

  # Inside the VM, format and mount the volume, then write some data to it.
  if [ "${isRemoteDriver}" = true ]; then
    LXD_DIR="${LXD_ONE_DIR}" lxc exec vm -- mkfs -t ext4 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_vmdata
    LXD_DIR="${LXD_ONE_DIR}" lxc exec vm -- mkdir /mnt/vol1
    LXD_DIR="${LXD_ONE_DIR}" lxc exec vm -- mount -t ext4 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_vmdata /mnt/vol1
    LXD_DIR="${LXD_ONE_DIR}" lxc exec vm -- cp /etc/hostname /mnt/vol1/bar
  fi

  # Perform live migration of the VM from node1 to node2.
  echo "Live migrating instance 'vm' ..."
  LXD_DIR="${LXD_ONE_DIR}" lxc move vm --target node2
  LXD_DIR="${LXD_ONE_DIR}" waitInstanceReady vm

  # After live migration, the volume should be functional and mounted.
  # Check that the file we created is still there with the same contents.
  if [ "${isRemoteDriver}" = true ]; then
    echo "Verifying data integrity after live migration"
    [ "$(LXD_DIR=${LXD_ONE_DIR} lxc exec vm -- cat /mnt/vol1/bar)" = "vm" ]
  fi

  # Cleanup
  echo "Cleaning up ..."
  unset LXD_TEST_LIVE_MIGRATION_ON_THE_SAME_HOST
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete "$(LXD_DIR="${LXD_ONE_DIR}" lxc config get vm volatile.base_image)"
  LXD_DIR="${LXD_ONE_DIR}" lxc delete --force vm

  if [ "${isRemoteDriver}" = true ]; then
    LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete "${poolName}" vmdata
  fi

  # Ensure cleanup of the cluster's data pool to not leave any traces behind when we are using a different driver besides dir.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete "${poolName}"

  lxc remote remove cls

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}
