# _nbd_export starts an NBD export of a storage volume in the background and sets NBD_PID, NBD_URI and
# NBD_STDERR once the export prints the address it listens on. The export contacts LXD only when a client
# connects, so a precondition error surfaces in NBD_STDERR after the first client. The lxc wrapper kills its
# command after 120s, which a full copy of the root disk can exceed, so the binary is called directly.
_nbd_export() {
  local output address
  output="$(mktemp -p "${TEST_DIR}" nbd_output.XXX)"
  NBD_STDERR="$(mktemp -p "${TEST_DIR}" nbd_stderr.XXX)"

  "${_LXC}" storage volume nbd "$@" > "${output}" 2> "${NBD_STDERR}" &
  NBD_PID=$!

  for _ in $(seq 60); do
    grep -qF "NBD listening on " "${output}" && break
    sleep 0.5
  done

  address="$(sed -n 's/^NBD listening on //p' "${output}")"
  rm "${output}"
  [ -n "${address}" ]
  NBD_URI="nbd://${address}"
}

test_storage_block_tracking_vm() {
  if ! check_dependencies nbdinfo nbdcopy qemu-nbd; then
    export TEST_UNMET_REQUIREMENT="Missing nbdinfo, nbdcopy or qemu-nbd"
    return
  fi

  local pool orig_volume_size count root_dev root_size root_copy checksum address first_pid holder_pid operation_uuid
  pool="lxdtest-$(basename "${LXD_DIR}")"
  orig_volume_size="$(lxc storage get "${pool}" volume.size)"
  if [ -n "${orig_volume_size:-}" ]; then
    # Override the volume.size to accommodate a VM
    lxc storage set "${pool}" volume.size "${SMALLEST_VM_ROOT_DISK}"
  fi

  ensure_import_ubuntu_vm_image

  lxc init ubuntu-vm v1 --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"
  lxc storage volume create "${pool}" cbt-blk size=32MiB --type block
  lxc storage volume attach "${pool}" cbt-blk v1
  lxc start v1
  waitInstanceReady v1

  setup_instance_gocoverage v1

  sub_test "Bitmap lifecycle on the root volume"
  lxc storage volume bitmap create "${pool}" virtual-machine/v1 bm1
  lxc storage volume bitmap list "${pool}" virtual-machine/v1 --format csv | grep -F "bm1"
  lxc storage volume bitmap show "${pool}" virtual-machine/v1 bm1 | yq --exit-status '.name == "bm1" and .busy == false and .granularity > 0'
  count="$(lxc storage volume bitmap show "${pool}" virtual-machine/v1 bm1 | yq --exit-status '.count')"
  [ "$(! "${_LXC}" storage volume bitmap create "${pool}" virtual-machine/v1 bm1 2>&1 1>/dev/null)" = 'Error: Bitmap "bm1" already exists on disk device "root"' ]
  [ "$(! "${_LXC}" storage volume bitmap show "${pool}" virtual-machine/v1 missing 2>&1 1>/dev/null)" = "Error: Bitmap not found" ]

  # Dirty a few MiB of the root disk and check that the bitmap recorded them.
  lxc exec v1 -- sh -c 'dd if=/dev/urandom of=/root/cbt.bin bs=1M count=4 && sync'
  [ "$(lxc storage volume bitmap show "${pool}" virtual-machine/v1 bm1 | yq --exit-status '.count')" -gt "${count}" ]

  sub_test "Read-only NBD export of the root volume"
  root_dev="/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_root"
  root_size="$(lxc exec v1 -- blockdev --getsize64 "${root_dev}")"
  checksum="$(lxc exec v1 -- sha256sum /root/cbt.bin)"

  # The export is read-only, has the size of the disk and publishes the bitmap as a metadata context.
  _nbd_export "${pool}" virtual-machine/v1
  nbdinfo --json "${NBD_URI}" | jq --exit-status --argjson size "${root_size}" '.exports[0] | .is_read_only and (."export-size" == $size) and (.contexts | index("qemu:dirty-bitmap:bm1") != null)'
  wait "${NBD_PID}"

  # The dirty map holds at least one dirty extent (type 1) for the blocks written above.
  _nbd_export "${pool}" virtual-machine/v1
  nbdinfo --map=qemu:dirty-bitmap:bm1 --json "${NBD_URI}" | jq --exit-status 'any(.[]; .type == 1)'
  wait "${NBD_PID}"

  # The export relays a single client, so nbdcopy must not open parallel connections.
  root_copy="$(mktemp -p "${TEST_DIR}" root_copy.XXX)"
  _nbd_export "${pool}" virtual-machine/v1
  nbdcopy --connections=1 "${NBD_URI}" "${root_copy}"
  wait "${NBD_PID}"
  [ "$(stat -c %s "${root_copy}")" = "${root_size}" ]

  # The session has ended, so the bitmap is free again.
  lxc storage volume bitmap show "${pool}" virtual-machine/v1 bm1 | yq --exit-status '.busy == false'
  lxc storage volume bitmap create "${pool}" virtual-machine/v1 bm2
  lxc storage volume bitmap delete "${pool}" virtual-machine/v1 bm2

  sub_test "NBD export is listed as an operation and cancelling it ends the export"
  # A raw connection keeps the session open while the operation representing it is looked up and cancelled.
  # nc ignores its stdin so that it exits once the server closes the connection.
  _nbd_export "${pool}" virtual-machine/v1
  address="${NBD_URI#nbd://}"
  nc -d "${address%:*}" "${address##*:}" &
  holder_pid=$!
  operation_uuid=""
  for _ in $(seq 20); do
    operation_uuid="$(lxc operation list --format csv | grep -F "TASK,Exporting storage volume over NBD,RUNNING" | cut -d, -f1 || true)"
    [ -n "${operation_uuid}" ] && break
    sleep 0.5
  done
  [ -n "${operation_uuid}" ]
  lxc operation show "${operation_uuid}" | yq --exit-status ".metadata.entity_url == \"/1.0/storage-pools/${pool}/volumes/virtual-machine/v1\""

  # Cancelling the operation closes the connection, so the client and the export command exit on their own.
  lxc operation delete "${operation_uuid}"
  wait "${NBD_PID}"
  wait "${holder_pid}"
  for _ in $(seq 10); do
    lxc operation show "${operation_uuid}" | yq --exit-status '.status == "Cancelled"' > /dev/null && break
    sleep 0.5
  done
  lxc operation show "${operation_uuid}" | yq --exit-status '.status == "Cancelled"'

  # The session has ended, so a new export can be opened.
  _nbd_export "${pool}" virtual-machine/v1
  nbdinfo --json "${NBD_URI}" | jq --exit-status --argjson size "${root_size}" '.exports[0] | .is_read_only and (."export-size" == $size)'
  wait "${NBD_PID}"

  sub_test "Reusing a running NBD session"
  # A raw connection held open keeps the first session alive while a second listener attaches to it.
  _nbd_export "${pool}" virtual-machine/v1
  first_pid="${NBD_PID}"
  address="${NBD_URI#nbd://}"
  sleep 60 | nc "${address%:*}" "${address##*:}" &
  holder_pid=$!

  _nbd_export "${pool}" virtual-machine/v1 --reuse
  nbdinfo --json "${NBD_URI}" | jq --exit-status --argjson size "${root_size}" '.exports[0] | .is_read_only and (."export-size" == $size)'

  # A reused listener keeps accepting clients while the session lasts, so it is stopped explicitly.
  kill "${NBD_PID}" 2>/dev/null || true
  wait "${NBD_PID}" || true
  kill "${holder_pid}"
  wait "${holder_pid}" || true
  wait "${first_pid}"

  sub_test "Bitmaps on an attached custom block volume"
  lxc storage volume bitmap create "${pool}" cbt-blk bm1
  lxc storage volume bitmap list "${pool}" cbt-blk --format csv | grep -F "bm1"
  lxc storage volume bitmap show "${pool}" cbt-blk bm1 | yq --exit-status '.name == "bm1" and .busy == false'
  # The disk serial escapes "-" to "--", so the by-id link of cbt-blk carries a double dash.
  lxc exec v1 -- dd if=/dev/urandom of=/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_cbt--blk bs=1M count=4 conv=fsync
  [ "$(lxc storage volume bitmap show "${pool}" cbt-blk bm1 | yq --exit-status '.count')" -gt 0 ]

  sub_test "Instance bitmaps cover the root disk and the custom block volume"
  lxc bitmap v1 bm2
  lxc storage volume bitmap list "${pool}" virtual-machine/v1 --format csv | grep -F "bm2"
  lxc storage volume bitmap list "${pool}" cbt-blk --format csv | grep -F "bm2"
  ! lxc bitmap v1 bm2 || false

  lxc storage volume bitmap delete "${pool}" virtual-machine/v1 bm1
  lxc storage volume bitmap delete "${pool}" virtual-machine/v1 bm2
  lxc storage volume bitmap delete "${pool}" cbt-blk bm1
  lxc storage volume bitmap delete "${pool}" cbt-blk bm2
  [ "$(lxc storage volume bitmap list "${pool}" virtual-machine/v1 --format csv || echo fail)" = "" ]
  [ "$(lxc storage volume bitmap list "${pool}" cbt-blk --format csv || echo fail)" = "" ]
  [ "$(! "${_LXC}" storage volume bitmap delete "${pool}" virtual-machine/v1 bm1 2>&1 1>/dev/null)" = "Error: Bitmap not found" ]

  sub_test "Containers reject bitmaps"
  ensure_import_testimage
  lxc launch testimage c1
  [ "$(! "${_LXC}" bitmap c1 bm1 2>&1 1>/dev/null)" = "Error: Bitmaps are only supported by virtual machines" ]
  [ "$(! "${_LXC}" storage volume bitmap create "${pool}" container/c1 bm1 2>&1 1>/dev/null)" = 'Error: Invalid storage volume type "container"' ]
  lxc delete -f c1

  sub_test "Shared volumes reject bitmaps and NBD exports"
  lxc storage volume create "${pool}" cbt-shared size=32MiB --type block security.shared=true
  [ "$(! "${_LXC}" storage volume bitmap create "${pool}" cbt-shared bm1 2>&1 1>/dev/null)" = "Error: Bitmaps are not supported on shared volumes" ]
  _nbd_export "${pool}" cbt-shared
  ! nbdinfo "${NBD_URI}" || false
  ! wait "${NBD_PID}" || false
  [[ "$(cat "${NBD_STDERR}")" == "Error: NBD export is not supported on shared volumes"* ]]
  lxc storage volume delete "${pool}" cbt-shared

  sub_test "Writable NBD import of the stopped root volume"
  # Written after the export was taken, so it must be gone once the copy is written back.
  lxc exec v1 -- sh -c 'touch /root/after-export && sync'
  # The minimal image runs no logind, so a graceful stop via the ACPI power button never completes.
  lxc stop -f v1
  [ "$(! "${_LXC}" storage volume bitmap create "${pool}" virtual-machine/v1 bm1 2>&1 1>/dev/null)" = "Error: Creating a bitmap requires the instance to be running" ]

  _nbd_export "${pool}" virtual-machine/v1 --writable
  nbdcopy --connections=1 "${root_copy}" "${NBD_URI}"
  wait "${NBD_PID}"

  # The volume is released after the export's relay ends, so the first start attempt can race it.
  for _ in $(seq 10); do
    lxc start v1 && break
    sleep 1
  done
  waitInstanceReady v1
  [ "$(lxc exec v1 -- sha256sum /root/cbt.bin)" = "${checksum}" ]
  ! lxc exec v1 -- test -e /root/after-export || false

  # A writable export cannot be shared, and an accepted one is refused while the instance runs.
  [ "$(! "${_LXC}" storage volume nbd "${pool}" virtual-machine/v1 --reuse --writable 2>&1 1>/dev/null)" = "Error: Cannot set --reuse with --writable, a writable export cannot be shared" ]
  _nbd_export "${pool}" virtual-machine/v1 --writable
  ! nbdinfo "${NBD_URI}" || false
  ! wait "${NBD_PID}" || false
  [[ "$(cat "${NBD_STDERR}")" == "Error: Writable NBD requires the instance to be stopped"* ]]

  # Cleanup.
  rm -f "${root_copy}" "${TEST_DIR}"/nbd_stderr.*
  prepare_vm_for_hard_stop v1
  lxc delete -f v1
  lxc storage volume delete "${pool}" cbt-blk

  if [ -n "${orig_volume_size:-}" ]; then
    # Restore the volume.size.
    lxc storage set "${pool}" volume.size "${orig_volume_size}"
  fi
}
