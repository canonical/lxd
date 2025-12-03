#!/bin/bash

# test_clustering_live_migration spawns a 2-node LXD cluster, creates a virtual machine on top of it,
# creates and attaches a block volume to the virtual machine, writes some arbitrary data to the volume,
# and runs live migration. Success is determined by the data being intact after live migration.
test_clustering_live_migration() {
  # shellcheck disable=SC2034
  local LXD_DIR

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes.
  # Use the driver from the profile that is chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')
  if [ "${poolDriver:-}" != "ceph" ]; then
    echo "==> SKIP: test_live_migration_cluster currently only supports 'ceph', not ${poolDriver}"
    return
  fi

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}" "${poolDriver}"

  # Set up a TLS identity with admin permissions.
  LXD_DIR=${LXD_ONE_DIR} lxc auth group create live-migration
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add live-migration server admin

  token="$(LXD_DIR=${LXD_ONE_DIR} lxc auth identity create tls/live-migration --group=live-migration --quiet)"
  oldRemote="$(lxc remote get-default)"
  lxc remote add cls 100.64.1.101:8443 --token="${token}"
  lxc remote switch cls

  # Storage pool created when spawning LXD cluster is "data".
  poolName="data"

  ensure_import_ubuntu_vm_image

  if [ "${poolDriver}" != "dir" ]; then
    oldVolumeSize=$(lxc storage get "${poolName}" volume.size)
    lxc storage set "${poolName}" volume.size="${SMALLEST_VM_ROOT_DISK}"
  fi

  # Initialize the VM.
  lxc init ubuntu-vm vm \
    --vm \
    --config limits.cpu=2 \
    --config limits.memory=768MiB \
    --config migration.stateful=true \
    --device root,size="${SMALLEST_VM_ROOT_DISK}" \
    --config security.devlxd=false \
    --target node1

  # Attach the block volume to the VM.
  if [ "${poolDriver}" = "dir" ]; then
    lxc storage volume create "${poolName}" vmdata --type=block
  else
    lxc storage volume create "${poolName}" vmdata --type=block size="64MiB"
  fi

  lxc config device add vm vmdata disk pool="${poolName}" source=vmdata

  # Start the VM.
  lxc start vm
  waitInstanceReady vm

  # Inside the VM, format and mount the volume, then write some data to it.
  lxc exec vm -- mkfs -t ext4 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_vmdata
  lxc exec vm -- mkdir /mnt/vol1
  lxc exec vm -- mount -t ext4 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_vmdata /mnt/vol1
  lxc exec vm -- cp /etc/hostname /mnt/vol1/bar

  # Perform live migration of the VM from node1 to node2.
  echo "Live migrating instance 'vm' ..."
  lxc move vm --target node2
  waitInstanceReady vm

  # After live migration, the volume should be functional and mounted.
  # Check that the file we created is still there with the same contents.
  echo "Verifying data integrity after live migration"
  [ "$(lxc exec vm -- cat /mnt/vol1/bar)" = "vm" ]

  # Cleanup
  lxc delete --force vm
  lxc storage volume delete "${poolName}" vmdata

  if [ "${oldVolumeSize:-}" != "" ]; then
    lxc storage set "${poolName}" volume.size="${oldVolumeSize}"
  fi

  lxc remote switch "${oldRemote}"
  lxc remote remove cls

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  sleep 1
  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}
