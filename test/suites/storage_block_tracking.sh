# _nbd_serve runs an lxc NBD command in the background and sets NBD_PID, NBD_URI and NBD_STDERR
# once the command prints the address it listens on, and NBD_SESSIONS_BEFORE to the NBD sessions running
# before it. The export contacts LXD only when a client connects.
# A precondition error therefore surfaces in NBD_STDERR after the first client.
# A failed command also prints NBD_STDERR, because a client that fails on the closed connection
# ends the test before any wait on NBD_PID.
# The lxc wrapper kills its command after 120s, which a full copy of the root disk can exceed.
# Therefore, the binary is called directly.
_nbd_serve() {
  local output address
  NBD_SESSIONS_BEFORE="$(_nbd_sessions)"
  output="$(mktemp -p "${TEST_DIR}" nbd_output.XXX)"
  NBD_STDERR="$(mktemp -p "${TEST_DIR}" nbd_stderr.XXX)"

  ( "${_LXC}" "$@" > "${output}" 2> "${NBD_STDERR}" || { rc=$?; cat "${NBD_STDERR}" >&2; exit "${rc}"; } ) &
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

# _nbd_sessions prints the IDs of the running operations of NBD sessions, sorted.
_nbd_sessions() {
  lxc operation list --format json | jq --exit-status --raw-output '[.[] | select((.description == "Exporting instance snapshot over NBD" or .description == "Importing storage volume over NBD") and .status == "Running") | .id] | join("\n")' | sort
}

# _nbd_wait_sessions waits for the NBD sessions that were not running before the last _nbd_serve to end on the server.
# An NBD command exits once its connection closes, and the server releases the volumes of the session only after that.
# The wait keeps a delete, a rename, a new session or a start of the instance that follows from racing the release.
# The flush of a root volume written in full takes a while.
_nbd_wait_sessions() {
  local sessions
  for _ in $(seq 240); do
    sessions="$(_nbd_sessions)"
    [ -z "$(comm -13 <(echo "${NBD_SESSIONS_BEFORE}") <(echo "${sessions}") || echo fail)" ] && return 0
    sleep 0.5
  done

  return 1
}

# _nbd_hold keeps an NBD session of the export at NBD_URI open until _nbd_release runs.
# nc ignores its stdin, which makes it exit once the server closes the connection.
# The helper waits for an operation that was not running before it connected, which LXD lists once
# the export has mounted the volumes.
_nbd_hold() {
  local address before
  before="$(_nbd_sessions)"
  address="${NBD_URI#nbd://}"
  nc -d "${address%:*}" "${address##*:}" &
  NBD_HOLDER_PID=$!
  for _ in $(seq 60); do
    [ -n "$(comm -13 <(echo "${before}") <(_nbd_sessions))" ] && return 0
    sleep 0.5
  done

  return 1
}

# _nbd_confined checks that the NBD server of the session held by _nbd_hold runs under an AppArmor
# profile of its own in enforce mode. The server is given by the name of its command.
_nbd_confined() {
  local pid
  pid="$(pgrep -f "^$1 .*${LXD_DIR}/nbd/")"
  [[ "$(< "/proc/${pid}/attr/current")" == "lxd_$1-"*" (enforce)" ]]
}

# _nbd_release ends the session opened by _nbd_hold and waits for the export command to exit and
# for the session to end on the server.
_nbd_release() {
  kill "${NBD_HOLDER_PID}" 2>/dev/null || true
  wait "${NBD_HOLDER_PID}" || true
  wait "${NBD_PID}" || true
  _nbd_wait_sessions
}

# _nbd_wait waits for the command started by _nbd_serve to exit and for its sessions to end on the server.
_nbd_wait() {
  wait "${NBD_PID}"
  _nbd_wait_sessions
}

# _nbd_refused runs the given NBD command, connects a client and prints the error the command exits with.
# Its session variables are local, which keeps a session held by _nbd_hold intact.
_nbd_refused() {
  local NBD_PID NBD_URI NBD_STDERR NBD_SESSIONS_BEFORE
  _nbd_serve "$@"
  ! nbdinfo "${NBD_URI}" || false
  ! wait "${NBD_PID}" || false
  cat "${NBD_STDERR}"
}

# _nbd_contexts prints the metadata contexts of the named export of the snapshot NBD command given
# as the remaining arguments, as a JSON array, and waits for the command to exit and for its
# session to end.
# A caller compares the output, and bash discards the exit status of a command substitution.
# Therefore, the contexts are printed only once the wait has succeeded.
_nbd_contexts() {
  local export_name="$1" contexts
  shift
  _nbd_serve "$@"
  contexts="$(nbdinfo --list --json "${NBD_URI}" | jq --exit-status --compact-output --arg name "${export_name}" '[.exports[] | select(."export-name" == $name)][0].contexts')"
  _nbd_wait
  echo "${contexts}"
}

# _nbd_check_location requests the NBD export at the given API path with the given method from the
# unix socket of LXD.
# It checks that the Location header of the 101 Switching Protocols response is the URL of the
# operation of the session.
# curl reads the NBD handshake as the response body until the server closes the connection.
# Therefore, the helper cancels the operation to end the session.
_nbd_check_location() {
  local headers curl_pid operation_uuid
  headers="$(mktemp -p "${TEST_DIR}" nbd_headers.XXX)"
  NBD_SESSIONS_BEFORE="$(_nbd_sessions)"
  curl --silent --unix-socket "${LXD_DIR}/unix.socket" --request "$1" --header "Connection: Upgrade" --header "Upgrade: nbd" --dump-header "${headers}" --output /dev/null "lxd$2" &
  curl_pid=$!

  for _ in $(seq 60); do
    operation_uuid="$(comm -13 <(echo "${NBD_SESSIONS_BEFORE}") <(_nbd_sessions))"
    [ -n "${operation_uuid}" ] && grep -qxF $'\r' "${headers}" && break
    sleep 0.5
  done

  [ "$(sed -n 's/^Location: \(.*\)\r$/\1/p' "${headers}")" = "/1.0/operations/${operation_uuid}" ]
  lxc operation delete "${operation_uuid}"
  wait "${curl_pid}" || true
  _nbd_wait_sessions
  rm "${headers}"
}

# _bitmaps prints the bitmaps of an instance snapshot, or of an instance through the internal
# endpoint, as "name,device,recording" lines, sorted. The bitmap commands take a snapshot.
# Therefore, the internal endpoint is read with lxc query.
# The lines are joined by jq, which makes an empty list a valid result under --exit-status.
_bitmaps() {
  if [[ "$1" == */* ]]; then
    lxc bitmap list "$1" --format csv -c ndr | sort
  else
    lxc query "/internal/instances/$1/bitmaps?recursion=1" | jq --exit-status --raw-output '[.[] | .name as $name | .volumes[] | "\($name),\(.device),\(if .recording then "YES" else "NO" end)"] | sort | join("\n")'
  fi
}

# _bitmap_uuid prints the UUID of the named bitmap of an instance snapshot, or of an instance
# through the internal endpoint.
_bitmap_uuid() {
  if [[ "$1" == */* ]]; then
    lxc bitmap show "$1" "$2" | yq -r --exit-status '.uuid'
  else
    lxc query "/internal/instances/$1/bitmaps/$2" | jq --raw-output --exit-status '.uuid'
  fi
}

# _snapshot_uuid prints the instance snapshot UUID of a snapshot, the UUID of its root volume snapshot.
_snapshot_uuid() {
  lxc storage volume get "${pool}" "virtual-machine/$1" volatile.uuid
}

# _file_offset prints the offset on the root disk of the first block of the given file of the given
# instance, the partition start of the root filesystem plus the first physical block of the file.
_file_offset() {
  local part_start first_block
  part_start="$(lxc exec "$1" -- sh -c "cat \"/sys/class/block/\$(basename \"\$(findmnt -no SOURCE /)\")/start\"")"
  first_block="$(lxc exec "$1" -- filefrag -v -b4096 "$2" | awk '/^ *0:/ { sub(/\.\./, "", $4); print $4 }')"
  [ -n "${part_start}" ]
  [ -n "${first_block}" ]
  echo "$((part_start * 512 + first_block * 4096))"
}

# _volume_snapshot_of prints the name of the snapshot of the custom volume cbt-blk that was taken
# with the given instance snapshot, which the instance snapshot records in
# volatile.attached_volumes.
_volume_snapshot_of() {
  local uuid
  uuid="$(lxc config show "$1" | yq -r --exit-status '.config."volatile.attached_volumes"' | jq --raw-output '."cbt-blk"')"
  lxc query "/1.0/storage-pools/${pool}/volumes/custom/cbt-blk/snapshots?recursion=1" | jq --exit-status --raw-output --arg uuid "${uuid}" '.[] | select(.config."volatile.uuid" == $uuid) | .name | ltrimstr("cbt-blk/")'
}

# _wait_stopped waits for the stop hook of the instance to end.
# The state is STOPPED between the end of the QEMU process and the start of the hook too, and
# RUNNING while the hook holds the stop lock.
# Therefore, the hook is known to have ended once it has set volatile.last_state.power and the
# state is STOPPED after that.
_wait_stopped() {
  for _ in $(seq 60); do
    [ "$(lxc config get "$1" volatile.last_state.power)" = "STOPPED" ] && [ "$(lxc list -f csv -c s "$1")" = "STOPPED" ] && return 0
    sleep 1
  done

  return 1
}

# _instance_exec_after runs the given command in the given instance after the given number of
# seconds, from a transient timer of systemd-run.
# With the default dependencies the timer fires only once the instance has finished its early boot,
# which waits two minutes for the network.
# Therefore, the timer and its service are started without them.
_instance_exec_after() {
  local instance="$1" delay="$2"
  shift 2
  lxc exec "${instance}" -- systemd-run --quiet --no-block --on-active="${delay}" -p DefaultDependencies=no --timer-property=DefaultDependencies=no "$@"
}

# _write_overlay creates an overlay file for the volume of the given UUID on the live config volume
# of the given instance, with the 64 KiB pattern of 0xab bytes written at its start, as a failed
# commit leaves one.
# It fails unless the overlay file reads back the pattern.
_write_overlay() {
  local overlay="${LXD_DIR}/virtual-machines/$1/bitmaps/$2.overlay.qcow2"
  qemu-img create -f qcow2 "${overlay}" "$3" > /dev/null
  qemu-io -f qcow2 -c "write -P 0xab 0 64k" "${overlay}" > /dev/null
  qemu-io -r -U -f qcow2 -c "read -P 0xab 0 64k" "${overlay}" > /dev/null
}

# _metadata_image_info prints the data-file-raw flag, the allocated size and the bitmaps with their
# flags of the metadata image at the given path, as JSON.
# The image records a data file that no longer exists.
# The helper opens the image with the null-co driver as its data file.
_metadata_image_info() {
  qemu-img info --force-share --output=json --image-opts "driver=qcow2,file.filename=$1,data-file.driver=null-co" | jq --exit-status --compact-output '{raw: ."format-specific".data."data-file-raw", size: ."actual-size", bitmaps: [(."format-specific".data.bitmaps // [])[] | {name, flags}]}'
}

# _qemu_monitor runs the given QMP commands on the QEMU process of the given instance over its
# monitor socket and prints the responses.
# It is used while LXD is not running, as the socket serves one client at a time.
_qemu_monitor() {
  local instance="$1"
  shift
  {
    echo '{"execute":"qmp_capabilities"}'
    printf '%s\n' "$@"
  } | nc -U -q 1 "${LXD_DIR}/logs/${instance}/qemu.monitor"
}

# _qemu_run_state prints the run state of the QEMU process of the given instance.
_qemu_run_state() {
  _qemu_monitor "$1" '{"execute":"query-status"}' | jq --raw-output --exit-status 'select(.return.status != null) | .return.status'
}

# _qemu_add_overlay adds the qcow2 file at the given path as the overlay node over the disk node of
# the device of the given instance with the given node name suffix, as a snapshot with a bitmap
# whose commit failed leaves one.
# QEMU runs as an unprivileged user that cannot open the file by its path.
# Therefore, the file is passed as a descriptor over the monitor socket, as LXD passes it.
_qemu_add_overlay() {
  python3 - "${LXD_DIR}/logs/$1/qemu.monitor" "$2" "$3" << 'EOF'
import json
import os
import socket
import sys

monitor, path, device = sys.argv[1:]
sock = socket.socket(socket.AF_UNIX)
sock.connect(monitor)
responses = sock.makefile("r")
responses.readline()


def run(command, arguments, fd=None):
    message = json.dumps({"execute": command, "arguments": arguments}).encode()
    if fd is None:
        sock.sendall(message)
    else:
        socket.send_fds(sock, [message], [fd])

    while True:
        response = json.loads(responses.readline())
        if "event" in response:
            continue

        if "error" in response:
            sys.exit(command + ": " + response["error"]["desc"])

        return response["return"]


node = "lxdoverlay_" + device
run("qmp_capabilities", {})
fdset = run("add-fd", {"opaque": "rdwr:" + node}, os.open(path, os.O_RDWR))["fdset-id"]
run("blockdev-add", {"driver": "qcow2", "node-name": node, "file": {"driver": "file", "filename": "/dev/fdset/" + str(fdset), "locking": "off"}, "backing": None})
run("blockdev-snapshot", {"node": "lxd_" + device, "overlay": node})
EOF
}

# _mount_config_snapshot mounts the config volume snapshot of the given snapshot of the given
# instance read-only at the given directory, through the storage backend.
# This lets the test read the metadata images that the snapshot was taken with.
# It fails on a backend that it has no case for. _umount_config_snapshot undoes it.
_mount_config_snapshot() {
  mkdir -p "$3"
  case "${lxd_backend}" in
    lvm)
      lvchange -ay -K "${pool}/virtual-machines_$1-$2"
      mount -o ro "/dev/${pool}/virtual-machines_$1-$2" "$3"
      ;;
    zfs)
      mount -t zfs -o ro "${pool}/virtual-machines/$1@snapshot-$2" "$3"
      ;;
    btrfs)
      mount --bind -o ro "${LXD_DIR}/storage-pools/${pool}/virtual-machines-snapshots/$1/$2" "$3"
      ;;
    *)
      rmdir "$3"
      return 1
      ;;
  esac
}

_umount_config_snapshot() {
  umount "$3"
  rmdir "$3"
  if [ "${lxd_backend}" = "lvm" ]; then
    lvchange -an "${pool}/virtual-machines_$1-$2"
  fi
}

test_storage_block_tracking_vm() {
  local lxd_backend

  lxd_backend=$(storage_backend "${LXD_DIR}")
  if [ "${lxd_backend}" = "dir" ]; then
    # Don't run VM changed block tracking tests on storage drivers that perform snapshots by copying entire volume.
    # This takes a lot of space and time.
    export TEST_UNMET_REQUIREMENT="Changed block tracking is not tested on the ${lxd_backend} backend"
    return
  fi

  check_dependencies nbdinfo nbdcopy qemu-img qemu-io qemu-nbd qemu-storage-daemon

  local pool orig_volume_size root_dev root_size s1_uuid s1b_uuid s2_uuid s2b_uuid s3_uuid s5_uuid s1_copy s2_copy reconstructed extents offset length chunk address blk_dev blk_size blk_checksum blk_copy operation_uuid pid new_pid bitmaps_dir root_uuid blk_uuid pattern_checksum import_src snap_a snap_b bitmap_file data_uuid data_dev shared_uuid file_offset port blk_snap conflict
  pool="lxdtest-$(basename "${LXD_DIR}")"
  orig_volume_size="$(lxc storage get "${pool}" volume.size)"
  if [ -n "${orig_volume_size:-}" ]; then
    # Override the volume.size to accommodate a VM
    lxc storage set "${pool}" volume.size "${SMALLEST_VM_ROOT_DISK}"
  fi

  # The writable NBD import of the root disk and the import of the backup each write the whole root
  # disk, which allocates it in full, and the pool of the harness cannot hold two such disks beside
  # the image.
  if [ -n "$(lxc storage get "${pool}" size)" ]; then
    lxc storage set "${pool}" size=10GiB
  fi

  ensure_import_ubuntu_vm_image

  lxc init ubuntu-vm v1 --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"
  lxc storage volume create "${pool}" cbt-blk size=32MiB --type block
  lxc storage volume attach "${pool}" cbt-blk v1
  lxc start v1
  waitInstanceReady v1

  setup_instance_gocoverage v1

  bitmaps_dir="${LXD_DIR}/virtual-machines/v1/bitmaps"
  root_uuid="$(lxc storage volume get "${pool}" virtual-machine/v1 volatile.uuid)"
  blk_uuid="$(lxc storage volume get "${pool}" cbt-blk volatile.uuid)"

  # SHA-256 checksum of the 64 KiB pattern of 0xab bytes that the overlay tests write.
  pattern_checksum="7c56cd2bee665a1839e41377e70c4a00e688c2b31e6e25638185b5ad1b1537e1"

  sub_test "A virtual machine has no volume metadata image before its first snapshot with a bitmap"
  # The first snapshot with a bitmap creates the images.
  # Therefore, the config volume holds none before it, and the instance has no bitmap to list.
  # The live listing is an internal endpoint, and the bitmap commands take a snapshot.
  [ ! -e "${bitmaps_dir}" ]
  [ "$(_bitmaps v1 || echo fail)" = "" ]
  ! lxc query /1.0/instances/v1/bitmaps || false
  [ "$(! "${_LXC}" bitmap list v1 2>&1 1>/dev/null)" = "Error: Missing instance snapshot name" ]

  # Without an image QEMU exits on a guest reboot or poweroff on its own, as it does without
  # changed block tracking, and the stop hook runs on the exit.
  # The guest skips stopping its services, which the minimal image does not complete, and the agent
  # answers until the new QEMU process runs.
  pid="$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')"
  lxc exec v1 -- systemctl reboot --force || true
  for _ in $(seq 60); do
    [ "$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')" != "${pid}" ] && break
    sleep 1
  done

  [ "$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')" != "${pid}" ]
  waitInstanceReady v1
  lxc exec v1 -- systemctl poweroff --force || true
  _wait_stopped v1
  lxc start v1
  waitInstanceReady v1
  [ ! -e "${bitmaps_dir}" ]

  sub_test "A snapshot with a bitmap creates the bitmap on the root disk"
  lxc snapshot v1 s1 --bitmap
  [ "$(_bitmaps v1)" = "s1,root,YES" ]
  lxc query /internal/instances/v1/bitmaps/s1 | jq --exit-status '.name == "s1" and (.volumes | length) == 1 and .volumes[0].device == "root" and .volumes[0].type == "virtual-machine" and .volumes[0].name == "v1" and .volumes[0].recording == true and .volumes[0].granularity == 65536'
  [ "$(lxc query /internal/instances/v1/bitmaps/s1 | jq --raw-output --exit-status '.volumes[0].uuid')" = "${root_uuid}" ]
  [ "$(lxc query /internal/instances/v1/bitmaps/s1 | jq --raw-output --exit-status '.volumes[0].pool')" = "${pool}" ]
  [ "$(! "${_LXC}" query /internal/instances/v1/bitmaps/missing 2>&1 1>/dev/null)" = "Error: Bitmap not found" ]

  # The UUID of the bitmap is the instance snapshot UUID of the snapshot it was created with.
  s1_uuid="$(_bitmap_uuid v1 s1)"
  [ "${s1_uuid}" = "$(_snapshot_uuid v1/s1)" ]

  # The snapshot bitmap file is removed from the config volume once the config volume snapshot
  # includes it, and the snapshot covers the root disk alone.
  # Its image is therefore the only file there. An image stores bitmaps only.
  # It has no preallocated tables and takes a few hundred KiB.
  [ "$(find "${bitmaps_dir}" -mindepth 1 -printf '%f\n')" = "${root_uuid}.qcow2" ]
  _metadata_image_info "${bitmaps_dir}/${root_uuid}.qcow2" | jq --exit-status '.raw == false and .size < 1048576 and .bitmaps == [{"name": "s1", "flags": ["in-use"]}]'

  # A snapshot with a bitmap of a name that exists fails as one without a bitmap does.
  [ "$(! "${_LXC}" snapshot v1 s1 --bitmap 2>&1 1>/dev/null)" = 'Error: Failed creating instance snapshot record "s1": Snapshot "v1/s1" already exists' ]
  [ "$(! "${_LXC}" snapshot v1 s1 2>&1 1>/dev/null)" = 'Error: Failed creating instance snapshot record "s1": Snapshot "v1/s1" already exists' ]

  # Saving the state of the guest removes the store bitmaps from the metadata disk nodes until the
  # guest resumes, and the bitmap of a snapshot is created in between.
  [ "$(! "${_LXC}" snapshot v1 stateful --stateful --bitmap 2>&1 1>/dev/null)" = "Error: A snapshot with a bitmap cannot be stateful" ]
  [ "$(_bitmaps v1)" = "s1,root,YES" ]

  # The image of the first snapshot with a bitmap holds its own bitmap only.
  # Its export therefore offers the allocation map only.
  [ "$(_bitmaps v1/s1 || echo fail)" = "" ]
  [ "$(_nbd_contexts root nbd v1/s1)" = '["base:allocation"]' ]

  sub_test "A snapshot without a bitmap keeps the bitmaps and has no bitmap listing and no export"
  lxc snapshot v1 p1
  [ "$(_bitmaps v1)" = "s1,root,YES" ]
  [ "$(! "${_LXC}" bitmap list v1/p1 2>&1 1>/dev/null)" = "Error: Snapshot was not created with a bitmap" ]
  [ "$(! "${_LXC}" bitmap show v1/p1 s1 2>&1 1>/dev/null)" = "Error: Snapshot was not created with a bitmap" ]
  [[ "$(_nbd_refused nbd v1/p1)" == "Error: Snapshot was not created with a bitmap"* ]]

  # The config volume snapshot holds the live images, whose bitmaps are in use, and no snapshot bitmap file.
  if _mount_config_snapshot v1 p1 "${TEST_DIR}/p1-config"; then
    _metadata_image_info "${TEST_DIR}/p1-config/bitmaps/${root_uuid}.qcow2" | jq --exit-status '.bitmaps == [{"name": "s1", "flags": ["in-use"]}]'
    [ -z "$(find "${TEST_DIR}/p1-config/bitmaps" -name 'snapshot.*.yaml' || echo fail)" ]
    _umount_config_snapshot v1 p1 "${TEST_DIR}/p1-config"
  else
    echo "==> Skipping the check of the snapshot images on the ${lxd_backend} backend"
  fi

  sub_test "A leftover snapshot bitmap file is ignored and pruned"
  # A file that a failed snapshot left on the config volume records no snapshot that exists.
  # A snapshot taken over it is therefore not one created with a bitmap, and the next start removes the file.
  cat > "${bitmaps_dir}/snapshot.00000000-0000-4000-8000-000000000001.yaml" << EOF
snapshot:
  uuid: 00000000-0000-4000-8000-000000000001
volumes:
  root:
    uuid: ${root_uuid}
    bitmaps: []
EOF
  lxc snapshot v1 p2
  [ "$(! "${_LXC}" bitmap list v1/p2 2>&1 1>/dev/null)" = "Error: Snapshot was not created with a bitmap" ]
  [[ "$(_nbd_refused nbd v1/p2)" == "Error: Snapshot was not created with a bitmap"* ]]
  lxc delete v1/p2
  lxc stop -f v1
  lxc start v1
  waitInstanceReady v1
  [ ! -e "${bitmaps_dir}/snapshot.00000000-0000-4000-8000-000000000001.yaml" ]
  [ "$(_bitmaps v1)" = "s1,root,YES" ]

  # Dirty a few MiB of the root disk.
  lxc exec v1 -- sh -c 'dd if=/dev/urandom of=/root/cbt.bin bs=1M count=4 && sync'

  sub_test "The next snapshot gets a copy of the bitmap"
  lxc snapshot v1 s2 --bitmap
  s2_uuid="$(_bitmap_uuid v1 s2)"
  [ "${s2_uuid}" = "$(_snapshot_uuid v1/s2)" ]
  [ "${s2_uuid}" != "${s1_uuid}" ]
  [ "$(_bitmaps v1)" = "$(printf 's1,root,YES\ns2,root,YES')" ]
  [ "$(_bitmaps v1/s2)" = "s1,root,NO" ]
  [ "$(_bitmap_uuid v1/s2 s1)" = "${s1_uuid}" ]
  lxc bitmap show v1/s2 s1 | yq --exit-status '.volumes[0].device == "root" and .volumes[0].type == "virtual-machine" and .volumes[0].name == "v1" and .volumes[0].recording == false'
  [ "$(! "${_LXC}" bitmap show v1/s2 s2 2>&1 1>/dev/null)" = "Error: Bitmap not found" ]
  [ "$(! "${_LXC}" bitmap show v1/missing s1 2>&1 1>/dev/null)" = 'Error: Failed fetching snapshot "missing" of instance "v1" in project "default": InstanceSnapshot not found' ]

  # Without recursion the listing has the URL of every bitmap, which the completion of a bitmap name reads.
  lxc query /1.0/instances/v1/snapshots/s2/bitmaps | jq --exit-status '. == ["/1.0/instances/v1/snapshots/s2/bitmaps/s1"]'
  [ "$("${_LXC}" __complete bitmap show v1/s2 "" 2>/dev/null | head -n -1)" = "s1" ]

  sub_test "The snapshot export exposes the bitmaps of the snapshot"
  root_dev="/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_root"
  root_size="$(lxc exec v1 -- blockdev --getsize64 "${root_dev}")"

  # The export is read-only, has the size of the disk and exposes the bitmap of the snapshot, but
  # not the bitmap created with the snapshot.
  _nbd_serve nbd v1/s2
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status --argjson size "${root_size}" '[.exports[] | select(."export-name" == "root")][0] | .is_read_only and (."export-size" == $size) and (.contexts == ["base:allocation", "qemu:dirty-bitmap:s1"])'
  _nbd_wait

  # The previous snapshot UUID limits the exposed bitmaps to the ones created with that snapshot,
  # and an unknown UUID exposes none.
  [ "$(_nbd_contexts root nbd v1/s2 --previous-snapshot-uuid "${s1_uuid}")" = '["base:allocation","qemu:dirty-bitmap:s1"]' ]
  [ "$(_nbd_contexts root nbd v1/s2 --previous-snapshot-uuid "${s2_uuid}")" = '["base:allocation"]' ]
  [ "$(_nbd_contexts root nbd v1/s2 --previous-snapshot-uuid 00000000-0000-0000-0000-000000000000)" = '["base:allocation"]' ]

  # A device that is not part of the snapshot is not found.
  [[ "$(_nbd_refused nbd v1/s2 --devices missing)" == 'Error: Snapshot has no volume snapshot with bitmaps for device "missing"'* ]]

  sub_test "An incremental backup reconstructs the second snapshot from the first"
  s1_copy="$(mktemp -p "${TEST_DIR}" s1_copy.XXX)"
  s2_copy="$(mktemp -p "${TEST_DIR}" s2_copy.XXX)"
  reconstructed="$(mktemp -p "${TEST_DIR}" reconstructed.XXX)"

  # Full copy of s1, then the blocks that the bitmap s1 marks in s2 on top of it.
  _nbd_serve nbd v1/s1
  nbdcopy --connections=1 "${NBD_URI}/root" "${s1_copy}"
  _nbd_wait
  [ "$(stat -c %s "${s1_copy}")" = "${root_size}" ]

  # Every export serves a single client. Each extent is therefore read in a session of its own.
  cp "${s1_copy}" "${reconstructed}"
  _nbd_serve nbd v1/s2 --previous-snapshot-uuid "${s1_uuid}"
  extents="$(nbdinfo --json --map=qemu:dirty-bitmap:s1 "${NBD_URI}/root" | jq --exit-status --compact-output '[.[] | select(.type == 1) | [.offset, .length]]')"
  _nbd_wait
  [ "$(echo "${extents}" | jq --exit-status 'length')" -gt 0 ]
  chunk="$(mktemp -p "${TEST_DIR}" chunk.XXX)"
  while read -r offset length; do
    _nbd_serve nbd v1/s2
    address="${NBD_URI#nbd://}"
    qemu-img convert --image-opts "driver=raw,offset=${offset},size=${length},file.driver=nbd,file.server.type=inet,file.server.host=${address%:*},file.server.port=${address##*:},file.export=root" -O raw "${chunk}"
    _nbd_wait
    dd if="${chunk}" of="${reconstructed}" bs=64K seek="$((offset / 65536))" conv=notrunc status=none
  done < <(echo "${extents}" | jq --exit-status --raw-output '.[] | "\(.[0]) \(.[1])"')
  rm -f "${chunk}"

  _nbd_serve nbd v1/s2
  nbdcopy --connections=1 "${NBD_URI}/root" "${s2_copy}"
  _nbd_wait
  [ "$(sha256sum "${reconstructed}" | cut -d' ' -f1)" = "$(sha256sum "${s2_copy}" | cut -d' ' -f1)" ]
  rm -f "${reconstructed}" "${s2_copy}"

  sub_test "Every client opens its own session"
  _nbd_serve nbd v1/s2
  _nbd_hold
  [ "$(_nbd_contexts root nbd v1/s2)" = '["base:allocation","qemu:dirty-bitmap:s1"]' ]
  _nbd_release

  sub_test "The server of a snapshot export runs under its own AppArmor profile, which is deleted with the session"
  _nbd_serve nbd v1/s2
  _nbd_hold
  _nbd_confined qemu-storage-daemon
  _nbd_release
  [ -z "$(find "${LXD_DIR}/security/apparmor/profiles" -name 'lxd_qemu-storage-daemon-*' || echo fail)" ]

  sub_test "NBD export is listed as an operation and cancelling it ends the export"
  _nbd_serve nbd v1/s2
  _nbd_hold
  operation_uuid="$(lxc operation list --format json | jq --exit-status --raw-output '[.[] | select(.description == "Exporting instance snapshot over NBD" and .status == "Running")][0].id')"
  [ -n "${operation_uuid}" ]
  lxc operation delete "${operation_uuid}"
  _nbd_release
  _nbd_check_location GET /1.0/instances/v1/snapshots/s2/nbd

  sub_test "The NBD commands listen on a given TCP address or unix socket"
  port="$(local_tcp_port)"
  _nbd_serve nbd v1/s2 --address "127.0.0.1:${port}"
  [ "${NBD_URI}" = "nbd://127.0.0.1:${port}" ]
  [ "$(nbdinfo --list --json "${NBD_URI}" | jq --exit-status --raw-output '.exports[]."export-name"')" = "root" ]
  _nbd_wait
  _nbd_serve nbd v1/s2 --address "${TEST_DIR}/nbd.sock"
  [ "$(nbdinfo --list --json "nbd+unix:///?socket=${TEST_DIR}/nbd.sock" | jq --exit-status --raw-output '.exports[]."export-name"')" = "root" ]
  _nbd_wait
  [ ! -e "${TEST_DIR}/nbd.sock" ]

  sub_test "Deleting a snapshot removes the bitmap of its name"
  lxc delete v1/s1
  [ "$(_bitmaps v1)" = "s2,root,YES" ]

  # The copies of the snapshots stay, and the export exposes the copy of a deleted snapshot by its UUID.
  [ "$(_bitmaps v1/s2)" = "s1,root,NO" ]
  [ "$(_bitmap_uuid v1/s2 s1)" = "${s1_uuid}" ]
  [ "$(_nbd_contexts root nbd v1/s2 --previous-snapshot-uuid "${s1_uuid}")" = '["base:allocation","qemu:dirty-bitmap:s1"]' ]

  sub_test "A snapshot deleted and created again under the same name gets a new UUID"
  lxc snapshot v1 s1 --bitmap
  s1b_uuid="$(_bitmap_uuid v1 s1)"
  [ "${s1b_uuid}" != "${s1_uuid}" ]

  # A file written between the two snapshots.
  lxc exec v1 -- sh -c 'head -c 65536 /dev/urandom > /root/cbt-s3.bin && sync'
  file_offset="$(_file_offset v1 /root/cbt-s3.bin)"
  lxc snapshot v1 s3 --bitmap
  s3_uuid="$(_bitmap_uuid v1 s3)"
  [ "$(_bitmaps v1)" = "$(printf 's1,root,YES\ns2,root,YES\ns3,root,YES')" ]
  [ "$(_bitmaps v1/s3)" = "$(printf 's1,root,NO\ns2,root,NO')" ]
  [ "$(_bitmap_uuid v1/s3 s1)" = "${s1b_uuid}" ]
  [ "$(_bitmap_uuid v1/s3 s2)" = "${s2_uuid}" ]
  [ "$(_nbd_contexts root nbd v1/s3)" = '["base:allocation","qemu:dirty-bitmap:s1","qemu:dirty-bitmap:s2"]' ]
  [ "$(_nbd_contexts root nbd v1/s3 --previous-snapshot-uuid "${s1_uuid}")" = '["base:allocation"]' ]
  [ "$(_nbd_contexts root nbd v1/s3 --previous-snapshot-uuid "${s1b_uuid}")" = '["base:allocation","qemu:dirty-bitmap:s1"]' ]
  [ "$(_nbd_contexts root nbd v1/s3 --previous-snapshot-uuid "${s2_uuid}")" = '["base:allocation","qemu:dirty-bitmap:s2"]' ]

  # The guest writes to its root disk on its own.
  # The bitmap is therefore checked for the block of the file only.
  # That a bitmap without writes has no dirty block is checked on a custom volume in "Every
  # attached custom volume has its own bitmaps".
  _nbd_serve nbd v1/s3 --previous-snapshot-uuid "${s1b_uuid}"
  nbdinfo --json --map=qemu:dirty-bitmap:s1 "${NBD_URI}/root" | jq --exit-status --argjson offset "${file_offset}" 'any(.[]; .type == 1 and .offset <= $offset and $offset < .offset + .length)'
  _nbd_wait

  sub_test "Renaming a snapshot removes the bitmaps of its old and new names"
  lxc move v1/s2 v1/s2b
  [ "$(_bitmaps v1)" = "$(printf 's1,root,YES\ns3,root,YES')" ]

  # The bitmap s2 of s3 refers to the snapshot now named s2b, whose UUID did not change.
  [ "$(_bitmap_uuid v1/s3 s2)" = "${s2_uuid}" ]
  [ "$(_snapshot_uuid v1/s2b)" = "${s2_uuid}" ]
  [ "$(_nbd_contexts root nbd v1/s3 --previous-snapshot-uuid "${s2_uuid}")" = '["base:allocation","qemu:dirty-bitmap:s2"]' ]

  # The renamed snapshot is listed and exported from the file of its UUID, which the rename does not change.
  [ "$(_bitmaps v1/s2b)" = "s1,root,NO" ]
  [ "$(_nbd_contexts root nbd v1/s2b)" = '["base:allocation","qemu:dirty-bitmap:s1"]' ]

  # The old name can be used again. The bitmap s2 of s3 does not refer to the new s2.
  lxc snapshot v1 s2 --bitmap
  s2b_uuid="$(_bitmap_uuid v1 s2)"
  [ "${s2b_uuid}" != "${s2_uuid}" ]
  [ "$(_bitmaps v1)" = "$(printf 's1,root,YES\ns2,root,YES\ns3,root,YES')" ]
  [ "$(_nbd_contexts root nbd v1/s3 --previous-snapshot-uuid "${s2b_uuid}")" = '["base:allocation"]' ]

  # A name moved to an older snapshot.
  # The listing of s3 keeps the UUID of the snapshot the bitmap was created with.
  lxc delete v1/s2
  lxc move v1/s1 v1/s2
  [ "$(_bitmaps v1)" = "s3,root,YES" ]
  [ "$(_bitmap_uuid v1/s3 s2)" = "${s2_uuid}" ]
  [ "$(_bitmap_uuid v1/s3 s1)" = "${s1b_uuid}" ]
  [ "$(_nbd_contexts root nbd v1/s3 --previous-snapshot-uuid "${s1b_uuid}")" = '["base:allocation","qemu:dirty-bitmap:s1"]' ]
  [ "$(_nbd_contexts root nbd v1/s3 --previous-snapshot-uuid "${s2b_uuid}")" = '["base:allocation"]' ]
  lxc delete v1/s2 v1/s2b v1/p1

  sub_test "An instance snapshot with a bitmap covers the attached block volume"
  # The custom block volume is a raw attached device the guest never writes to on its own.
  # Its content is therefore stable and can be checksummed against the export.
  blk_dev="/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_cbt--blk"
  blk_size="$(lxc exec v1 -- blockdev --getsize64 "${blk_dev}")"
  blk_checksum="$(lxc exec v1 -- sha256sum "${blk_dev}" | cut -d' ' -f1)"

  # The first snapshot with a bitmap that covers the volume creates its image.
  [ ! -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  lxc snapshot v1 s4 --bitmap --disk-volumes all-exclusive
  [ -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES')" ]
  [ "$(_bitmaps v1/s4)" = "s3,root,NO" ]
  [ "$(lxc query /internal/instances/v1/bitmaps/s4 | jq --exit-status '.volumes | length')" = "2" ]
  [ "$(lxc query /internal/instances/v1/bitmaps/s4 | jq --raw-output --exit-status '.volumes[0].uuid')" = "${blk_uuid}" ]
  lxc query /internal/instances/v1/bitmaps/s4 | jq --exit-status '.volumes[0].device == "cbt-blk" and .volumes[0].type == "custom" and .volumes[0].name == "cbt-blk"'
  snap_a="$(_volume_snapshot_of v1/s4)"
  [ -n "${snap_a}" ]

  # Every volume of the snapshot is listed under an export named after its disk device, with its
  # own size and its own bitmaps of the snapshot.
  _nbd_serve nbd v1/s4
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status --argjson root "${root_size}" --argjson blk "${blk_size}" '
    ([.exports[] | select(."export-name" == "root")][0] | (."export-size" == $root) and (.contexts == ["base:allocation", "qemu:dirty-bitmap:s3"]))
    and ([.exports[] | select(."export-name" == "cbt-blk")][0] | (."export-size" == $blk) and (.contexts == ["base:allocation"]))'
  _nbd_wait

  # Selecting the custom disk export yields the guest's own view of the volume.
  blk_copy="$(mktemp -p "${TEST_DIR}" blk_copy.XXX)"
  _nbd_serve nbd v1/s4
  nbdcopy --connections=1 "${NBD_URI}/cbt-blk" "${blk_copy}"
  _nbd_wait
  [ "$(stat -c %s "${blk_copy}")" = "${blk_size}" ]
  [ "$(sha256sum "${blk_copy}" | cut -d' ' -f1)" = "${blk_checksum}" ]
  rm -f "${blk_copy}"

  # The devices filter serves a subset of the volumes.
  _nbd_serve nbd v1/s4 --devices cbt-blk
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status '(.exports | length) == 1 and .exports[0]."export-name" == "cbt-blk"'
  _nbd_wait

  sub_test "The next snapshot copies the bitmaps of both volumes"
  lxc snapshot v1 s5 --bitmap --disk-volumes all-exclusive
  snap_b="$(_volume_snapshot_of v1/s5)"
  [ "$(_bitmaps v1/s5)" = "$(printf 's3,root,NO\ns4,cbt-blk,NO\ns4,root,NO')" ]
  [ "$(_nbd_contexts cbt-blk nbd v1/s5)" = '["base:allocation","qemu:dirty-bitmap:s4"]' ]
  [ "$(_nbd_contexts cbt-blk nbd v1/s5 --previous-snapshot-uuid "$(_snapshot_uuid v1/s4)")" = '["base:allocation","qemu:dirty-bitmap:s4"]' ]

  sub_test "The snapshot bitmap file lists the disks the snapshot handled"
  # A snapshot of the root disk alone creates no bitmap on the attached volume.
  # Therefore, the file has an entry for the root disk only, and the export has no volume for the
  # device of the attached volume.
  # The image of that volume in the config volume snapshot is the live image, with its bitmaps in use.
  lxc snapshot v1 s5a --bitmap
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES\ns5a,root,YES')" ]
  [ "$(_bitmaps v1/s5a)" = "$(printf 's3,root,NO\ns4,root,NO\ns5,root,NO')" ]
  [[ "$(_nbd_refused nbd v1/s5a --devices cbt-blk)" == 'Error: Snapshot has no volume snapshot with bitmaps for device "cbt-blk"'* ]]
  if _mount_config_snapshot v1 s5a "${TEST_DIR}/s5a-config"; then
    [ "$(yq -r --exit-status '.volumes | keys | join(",")' < "${TEST_DIR}/s5a-config/bitmaps/snapshot.$(_snapshot_uuid v1/s5a).yaml")" = "root" ]
    _metadata_image_info "${TEST_DIR}/s5a-config/bitmaps/${blk_uuid}.qcow2" | jq --exit-status '(.bitmaps | sort_by(.name)) == [{"name": "s4", "flags": ["in-use"]}, {"name": "s5", "flags": ["in-use"]}]'
    _umount_config_snapshot v1 s5a "${TEST_DIR}/s5a-config"
  fi
  lxc delete v1/s5a
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]

  sub_test "The image of a snapshot holds the copies it was written with"
  # QEMU writes the bitmaps into the volume metadata image when the metadata disk node closes, and
  # LXD reads every image back before the storage snapshot.
  # The config volume snapshot therefore holds every bitmap, and none is in use.
  # The snapshot bitmap file next to the images records the snapshot and the bitmaps each image
  # holds, without the one created with the snapshot.
  if _mount_config_snapshot v1 s5 "${TEST_DIR}/s5-config"; then
    _metadata_image_info "${TEST_DIR}/s5-config/bitmaps/${root_uuid}.qcow2" | jq --exit-status '(.bitmaps | sort_by(.name)) == [{"name": "s3", "flags": []}, {"name": "s4", "flags": []}, {"name": "s5", "flags": []}]'
    _metadata_image_info "${TEST_DIR}/s5-config/bitmaps/${blk_uuid}.qcow2" | jq --exit-status '(.bitmaps | sort_by(.name)) == [{"name": "s4", "flags": []}, {"name": "s5", "flags": []}]'
    s5_uuid="$(_snapshot_uuid v1/s5)"
    bitmap_file="${TEST_DIR}/s5-config/bitmaps/snapshot.${s5_uuid}.yaml"
    [ "$(yq -r --exit-status '.snapshot.uuid' < "${bitmap_file}")" = "${s5_uuid}" ]
    [ "$(yq -r --exit-status '.volumes | keys | join(",")' < "${bitmap_file}")" = "cbt-blk,root" ]
    [ "$(yq -r --exit-status '.volumes.root.uuid' < "${bitmap_file}")" = "${root_uuid}" ]
    [ "$(yq -r --exit-status '.volumes.root.bitmaps | map(.name + ":" + .uuid) | join(",")' < "${bitmap_file}")" = "s3:${s3_uuid},s4:$(_snapshot_uuid v1/s4)" ]
    [ "$(yq -r --exit-status '.volumes.root.bitmaps | map(.granularity == 65536) | all' < "${bitmap_file}")" = "true" ]
    [ "$(yq -r --exit-status '.volumes."cbt-blk".uuid' < "${bitmap_file}")" = "${blk_uuid}" ]
    [ "$(yq -r --exit-status '.volumes."cbt-blk".bitmaps | map(.name + ":" + .uuid) | join(",")' < "${bitmap_file}")" = "s4:$(_snapshot_uuid v1/s4)" ]
    [ "$(yq -r --exit-status '.volumes."cbt-blk".bitmaps | map(.granularity == 65536) | all' < "${bitmap_file}")" = "true" ]
    # The overlays of the disks are on the config volume until the storage snapshot is taken.
    # The config volume snapshot therefore holds them next to the images and the file.
    [ "$(find "${TEST_DIR}/s5-config/bitmaps" -mindepth 1 -printf '%f\n' | sort)" = "$(printf '%s.overlay.qcow2\n%s.qcow2\n%s.overlay.qcow2\n%s.qcow2\nsnapshot.%s.yaml' "${blk_uuid}" "${blk_uuid}" "${root_uuid}" "${root_uuid}" "${s5_uuid}" | sort)" ]
    _umount_config_snapshot v1 s5 "${TEST_DIR}/s5-config"
  else
    echo "==> Skipping the check of the snapshot images on the ${lxd_backend} backend"
  fi

  sub_test "The config volume holds each bitmap once"
  # The images are written for the config volume snapshot, and the metadata disk nodes are added
  # back once the overlays are committed, which marks the bitmaps in use again.
  # The live config volume therefore holds one image per disk and nothing else.
  [ "$(find "${bitmaps_dir}" -mindepth 1 -printf '%f\n' | sort)" = "$(printf '%s.qcow2\n%s.qcow2' "${blk_uuid}" "${root_uuid}" | sort)" ]
  _metadata_image_info "${bitmaps_dir}/${root_uuid}.qcow2" | jq --exit-status '(.bitmaps | sort_by(.name)) == [{"name": "s3", "flags": ["in-use"]}, {"name": "s4", "flags": ["in-use"]}, {"name": "s5", "flags": ["in-use"]}]'
  _metadata_image_info "${bitmaps_dir}/${blk_uuid}.qcow2" | jq --exit-status '(.bitmaps | sort_by(.name)) == [{"name": "s4", "flags": ["in-use"]}, {"name": "s5", "flags": ["in-use"]}]'

  sub_test "Bitmaps are kept across a stop, a start and a reboot"
  # The minimal image runs no logind.
  # A graceful stop via the ACPI power button therefore never completes.
  lxc stop -f v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]
  [ "$(_bitmap_uuid v1 s3)" = "${s3_uuid}" ]
  lxc start v1
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]
  [ "$(_bitmap_uuid v1 s3)" = "${s3_uuid}" ]

  # A reboot from inside the guest pauses the QEMU process, and LXD merges the bitmaps into the
  # images and ends it before it starts a new one.
  pid="$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')"
  lxc exec v1 -- systemctl reboot --force || true
  for _ in $(seq 60); do
    [ "$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')" != "${pid}" ] && break
    sleep 1
  done

  [ "$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')" != "${pid}" ]
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]

  # A guest that powers itself off pauses the QEMU process the same way.
  lxc exec v1 -- systemctl poweroff --force || true
  _wait_stopped v1
  lxc start v1
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]

  sub_test "A guest poweroff after an LXD restart keeps the bitmaps"
  # The daemon is killed to keep the instance running, and the new daemon handles the shutdown event of the guest.
  kill -9 "$(< "${LXD_DIR}/lxd.pid")"
  respawn_lxd "${LXD_DIR}" true
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]
  lxc exec v1 -- systemctl poweroff --force || true
  _wait_stopped v1
  lxc start v1
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]

  sub_test "A guest poweroff while LXD is not running keeps the bitmaps"
  # QEMU pauses on the guest poweroff instead of exiting, and the process stays paused until a
  # daemon merges its bitmaps into the images and ends it.
  # The new daemon does so when it starts, and then starts the instance again because it was
  # running when the old daemon was killed.
  # The guest schedules the poweroff before the daemon is killed, as lxc exec needs the daemon.
  pid="$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')"
  _instance_exec_after v1 5 systemctl poweroff --force
  kill -9 "$(< "${LXD_DIR}/lxd.pid")"
  for _ in $(seq 60); do
    [ "$(_qemu_run_state v1)" = "shutdown" ] && break
    sleep 1
  done

  respawn_lxd "${LXD_DIR}" true
  for _ in $(seq 120); do
    new_pid="$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid' || echo 0)"
    [ "${new_pid}" -gt 0 ] && [ "${new_pid}" != "${pid}" ] && break
    sleep 1
  done

  [ "${new_pid}" -gt 0 ] && [ "${new_pid}" != "${pid}" ]
  ! kill -0 "${pid}" 2>/dev/null || false
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,YES\ns4,root,YES\ns5,cbt-blk,YES\ns5,root,YES')" ]

  sub_test "A stop leaves an overlay file, and the start commits it and deletes the image of its volume"
  # An overlay file left by a failed commit contains guest writes that the volume lacks.
  # The guest never writes to the custom block volume on its own.
  # The pattern is therefore what it must read back.
  # The stop leaves the file, and the stopped instance lists the bitmaps of the volume as not
  # recording, as they lack the writes of the overlay.
  # The start deletes the volume metadata image of the volume, keeps the other one and commits the file.
  _write_overlay v1 "${blk_uuid}" "${blk_size}"
  lxc stop -f v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,cbt-blk,NO\ns4,root,YES\ns5,cbt-blk,NO\ns5,root,YES')" ]
  lxc start v1
  waitInstanceReady v1
  [ "$(lxc exec v1 -- head -c 65536 "${blk_dev}" | sha256sum | cut -d' ' -f1)" = "${pattern_checksum}" ]
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,root,YES\ns5,root,YES')" ]
  lxc snapshot v1 s5b --bitmap --disk-volumes all-exclusive
  [ "$(_nbd_contexts cbt-blk nbd v1/s5b --previous-snapshot-uuid "$(_snapshot_uuid v1/s5)")" = '["base:allocation"]' ]
  lxc delete v1/s5b
  [ "$(_bitmaps v1)" = "$(printf 's3,root,YES\ns4,root,YES\ns5,root,YES')" ]

  sub_test "A crash invalidates the bitmaps and the next snapshot with a bitmap removes them"
  # The bitmaps miss the writes after the crash.
  # They are therefore listed as not recording until a snapshot with a bitmap removes them.
  # The start commits the overlay of the crash and deletes the volume metadata image of its volume.
  _write_overlay v1 "${blk_uuid}" "${blk_size}"
  pid="$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')"
  kill -9 "${pid}"
  _wait_stopped v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,NO\ns4,root,NO\ns5,root,NO')" ]
  lxc start v1
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's3,root,NO\ns4,root,NO\ns5,root,NO')" ]
  [ ! -e "${bitmaps_dir}/${blk_uuid}.overlay.qcow2" ]
  [ "$(lxc exec v1 -- head -c 65536 "${blk_dev}" | sha256sum | cut -d' ' -f1)" = "${pattern_checksum}" ]
  lxc snapshot v1 s6 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's6,cbt-blk,YES\ns6,root,YES')" ]
  [ "$(_bitmaps v1/s6 || echo fail)" = "" ]
  [ "$(_bitmaps v1/s5)" = "$(printf 's3,root,NO\ns4,cbt-blk,NO\ns4,root,NO')" ]

  sub_test "An LXD restart during the snapshot window keeps the bitmaps"
  # A daemon that dies during a snapshot with a bitmap leaves the instance running without its
  # metadata disk nodes, which is emulated over the monitor socket.
  # The disk bitmaps keep recording, and the new daemon adds the metadata disk nodes back when it
  # commits the overlays of the running instances.
  # The write of the guest is scheduled before the daemon is killed, as the poweroff above.
  _instance_exec_after v1 5 dd if=/dev/urandom of="${blk_dev}" bs=64K count=1 seek=1 conv=fsync
  kill -9 "$(< "${LXD_DIR}/lxd.pid")"
  _qemu_monitor v1 '{"execute":"blockdev-del","arguments":{"node-name":"lxdimage_root"}}' '{"execute":"blockdev-del","arguments":{"node-name":"lxdimage_cbt--blk"}}' | jq --exit-status --slurp 'all(.error == null)'
  for _ in $(seq 60); do
    _qemu_monitor v1 '{"execute":"query-named-block-nodes"}' | jq --exit-status '.return[]? | select(."node-name" == "lxd_cbt--blk") | ."dirty-bitmaps"[] | select(.name == "s6") | .count > 0' > /dev/null && break
    sleep 1
  done

  respawn_lxd "${LXD_DIR}" true
  for _ in $(seq 60); do
    [ "$(_bitmaps v1 || true)" = "$(printf 's6,cbt-blk,YES\ns6,root,YES')" ] && break
    sleep 1
  done

  [ "$(_bitmaps v1)" = "$(printf 's6,cbt-blk,YES\ns6,root,YES')" ]
  lxc stop -f v1
  lxc start v1
  waitInstanceReady v1
  lxc snapshot v1 s6b --bitmap --disk-volumes all-exclusive
  _nbd_serve nbd v1/s6b --previous-snapshot-uuid "$(_snapshot_uuid v1/s6)"
  nbdinfo --json --map=qemu:dirty-bitmap:s6 "${NBD_URI}/cbt-blk" | jq --exit-status '[.[] | select(.type == 1) | [.offset, .length]] == [[65536, 65536]]'
  _nbd_wait
  lxc delete v1/s6b
  [ "$(_bitmaps v1)" = "$(printf 's6,cbt-blk,YES\ns6,root,YES')" ]

  sub_test "A guest poweroff during a snapshot with a bitmap keeps the bitmaps"
  # The handler of the poweroff waits for the snapshot, which commits its overlay on the paused
  # process, before it ends the process. The poweroff is not forced into the window.
  # The snapshot may therefore also fail on the stopped instance, and the bitmaps are kept either way.
  _instance_exec_after v1 1 systemctl poweroff --force
  lxc snapshot v1 s7 --bitmap || true
  _wait_stopped v1
  if lxc query /1.0/instances/v1/snapshots | jq --exit-status 'any(.[]; . == "/1.0/instances/v1/snapshots/s7")'; then
    [ "$(_bitmaps v1)" = "$(printf 's6,cbt-blk,YES\ns6,root,YES\ns7,root,YES')" ]
    [ "$(_nbd_contexts root nbd v1/s7 --previous-snapshot-uuid "$(_snapshot_uuid v1/s6)")" = '["base:allocation","qemu:dirty-bitmap:s6"]' ]
    lxc delete v1/s7
  fi

  [ "$(_bitmaps v1)" = "$(printf 's6,cbt-blk,YES\ns6,root,YES')" ]
  lxc start v1
  waitInstanceReady v1

  sub_test "A guest poweroff leaves an overlay for the start, which deletes the bitmaps of its volume"
  # A snapshot with a bitmap whose commit failed leaves the overlay node on the disk, which is
  # emulated over the monitor socket while LXD is not running.
  # The guest writes the pattern to the fourth block of the custom block volume through the overlay
  # and powers off, and the new daemon stops the paused process without committing the overlay.
  # The stop removes the bitmaps of the volume from its image, as they lack the write, and the
  # start that follows commits the overlay file and deletes the image.
  pid="$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid')"
  qemu-img create -f qcow2 "${bitmaps_dir}/${blk_uuid}.overlay.qcow2" "${blk_size}" > /dev/null
  _instance_exec_after v1 5 sh -c "head -c 65536 /dev/zero | tr '\\0' '\\253' | dd of=${blk_dev} bs=64K count=1 seek=3 iflag=fullblock conv=fsync && systemctl poweroff --force"
  kill -9 "$(< "${LXD_DIR}/lxd.pid")"
  _qemu_add_overlay v1 "${bitmaps_dir}/${blk_uuid}.overlay.qcow2" cbt--blk
  for _ in $(seq 60); do
    [ "$(_qemu_run_state v1)" = "shutdown" ] && break
    sleep 1
  done

  [ "$(_qemu_run_state v1)" = "shutdown" ]
  qemu-img map --force-share --output=json -f qcow2 "${bitmaps_dir}/${blk_uuid}.overlay.qcow2" | jq --exit-status 'any(.[]; .data and .start <= 196608 and 196608 < .start + .length)'
  qemu-io -r -U -f qcow2 -c "read -P 0xab 196608 64k" "${bitmaps_dir}/${blk_uuid}.overlay.qcow2" > /dev/null
  respawn_lxd "${LXD_DIR}" true
  for _ in $(seq 120); do
    new_pid="$(lxc query /1.0/instances/v1/state | jq --exit-status '.pid' || echo 0)"
    [ "${new_pid}" -gt 0 ] && [ "${new_pid}" != "${pid}" ] && break
    sleep 1
  done

  [ "${new_pid}" -gt 0 ] && [ "${new_pid}" != "${pid}" ]
  waitInstanceReady v1
  [ ! -e "${bitmaps_dir}/${blk_uuid}.overlay.qcow2" ]
  [ ! -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  [ "$(lxc exec v1 -- dd if="${blk_dev}" bs=64K count=1 skip=3 status=none | sha256sum | cut -d' ' -f1)" = "${pattern_checksum}" ]
  [ "$(_bitmaps v1)" = "s6,root,YES" ]

  sub_test "Restoring a snapshot deletes the bitmaps of the volumes and keeps the copies of the snapshots"
  # The latest snapshot is restored, as ZFS restores no other without deleting the snapshots after it.
  lxc exec v1 -- sh -c 'echo restore > /root/cbt3.bin && sync'
  lxc stop -f v1
  lxc restore v1 s6
  lxc start v1
  waitInstanceReady v1
  [ "$(_bitmaps v1 || echo fail)" = "" ]
  [ "$(_bitmaps v1/s6 || echo fail)" = "" ]
  [ "$(_bitmaps v1/s5)" = "$(printf 's3,root,NO\ns4,cbt-blk,NO\ns4,root,NO')" ]
  ! lxc exec v1 -- test -e /root/cbt3.bin || false

  sub_test "A snapshot that is exported, its volume snapshots and its instance cannot be deleted or renamed"
  _nbd_serve nbd v1/s5
  _nbd_hold
  [ "$(! "${_LXC}" delete v1/s5 2>&1 1>/dev/null)" = 'Error: Failed deleting instance snapshot "v1/s5" in project "default": Snapshot "v1/s5" is exported over NBD: In use' ]
  [ "$(! "${_LXC}" move v1/s5 v1/s5-renamed 2>&1 1>/dev/null)" = 'Error: Snapshot "v1/s5" is exported over NBD: In use' ]
  [ "$(! "${_LXC}" storage volume delete "${pool}" "cbt-blk/${snap_b}" 2>&1 1>/dev/null)" = "Error: Snapshot \"cbt-blk/${snap_b}\" is exported over NBD: In use" ]
  [ "$(! "${_LXC}" storage volume rename "${pool}" "cbt-blk/${snap_b}" cbt-blk/renamed 2>&1 1>/dev/null)" = "Error: Snapshot \"cbt-blk/${snap_b}\" is exported over NBD: In use" ]

  # A delete or a rename needs the instance stopped.
  lxc stop -f v1
  [ "$(! "${_LXC}" delete v1 2>&1 1>/dev/null)" = 'Error: Failed deleting instance "v1" in project "default": A snapshot of "v1" is exported over NBD: In use' ]
  [ "$(! "${_LXC}" move v1 v1-renamed 2>&1 1>/dev/null)" = 'Error: A snapshot of "v1" is exported over NBD: In use' ]
  _nbd_release
  lxc start v1
  waitInstanceReady v1

  sub_test "Deleting a custom volume snapshot removes the bitmap created with it from that volume"
  lxc snapshot v1 s7 --bitmap --disk-volumes all-exclusive
  lxc snapshot v1 s8 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's7,cbt-blk,YES\ns7,root,YES\ns8,cbt-blk,YES\ns8,root,YES')" ]
  [ "$(_bitmaps v1/s8)" = "$(printf 's7,cbt-blk,NO\ns7,root,NO')" ]
  lxc storage volume delete "${pool}" "cbt-blk/$(_volume_snapshot_of v1/s7)"
  [ "$(_bitmaps v1)" = "$(printf 's7,root,YES\ns8,cbt-blk,YES\ns8,root,YES')" ]

  # The export of s8 keeps the copies of both volumes, as the snapshot metadata images are not modified.
  [ "$(_nbd_contexts cbt-blk nbd v1/s8 --previous-snapshot-uuid "$(_snapshot_uuid v1/s7)")" = '["base:allocation","qemu:dirty-bitmap:s7"]' ]

  # The next snapshot copies the bitmap of the root disk only.
  lxc snapshot v1 s9 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1/s9)" = "$(printf 's7,root,NO\ns8,cbt-blk,NO\ns8,root,NO')" ]

  # Renaming a custom volume snapshot keeps the bitmap, as the snapshot keeps its UUID.
  lxc storage volume rename "${pool}" "cbt-blk/$(_volume_snapshot_of v1/s8)" cbt-blk/s8-renamed
  [ "$(_bitmaps v1)" = "$(printf 's7,root,YES\ns8,cbt-blk,YES\ns8,root,YES\ns9,cbt-blk,YES\ns9,root,YES')" ]
  [ "$(_nbd_contexts cbt-blk nbd v1/s9)" = '["base:allocation","qemu:dirty-bitmap:s8"]' ]

  sub_test "Deleting the custom volume snapshot of an exported snapshot leaves the export without that volume"
  lxc storage volume delete "${pool}" "cbt-blk/${snap_b}"
  _nbd_serve nbd v1/s5
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status '(.exports | length) == 1 and .exports[0]."export-name" == "root"'
  _nbd_wait
  [[ "$(_nbd_refused nbd v1/s5 --devices cbt-blk)" == 'Error: Snapshot has no volume snapshot with bitmaps for device "cbt-blk"'* ]]
  [ "$(_bitmaps v1/s5)" = "$(printf 's3,root,NO\ns4,root,NO')" ]
  lxc delete v1/s5 v1/s6 v1/s7 v1/s8 v1/s9
  lxc storage volume delete "${pool}" "cbt-blk/${snap_a}"
  lxc storage volume delete "${pool}" cbt-blk/s8-renamed
  [ "$(_bitmaps v1 || echo fail)" = "" ]

  sub_test "Renaming a device keeps the bitmaps of its volume"
  lxc snapshot v1 s10 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk,YES\ns10,root,YES')" ]

  # Only the disk bitmap records a write of the run until it is merged into the store bitmap.
  # The rename removes the device and adds it again, and merges the bitmaps in between.
  lxc exec v1 -- dd if=/dev/urandom of="${blk_dev}" bs=64K count=1 seek=3 conv=fsync status=none
  lxc config show v1 | sed 's/^  cbt-blk:$/  cbt-blk2:/' | lxc config edit v1
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk2,YES\ns10,root,YES')" ]
  [ -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  lxc stop -f v1
  lxc config show v1 | sed 's/^  cbt-blk2:$/  cbt-blk:/' | lxc config edit v1
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk,YES\ns10,root,YES')" ]
  lxc start v1
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk,YES\ns10,root,YES')" ]

  # The export of a snapshot taken before the device rename is named after the device name at the time of the snapshot.
  [ "$(_nbd_contexts cbt-blk nbd v1/s10)" = '["base:allocation"]' ]

  # The bitmap has the write that the guest made before the rename.
  lxc snapshot v1 s10b --bitmap --disk-volumes all-exclusive
  _nbd_serve nbd v1/s10b --previous-snapshot-uuid "$(_snapshot_uuid v1/s10)"
  nbdinfo --json --map=qemu:dirty-bitmap:s10 "${NBD_URI}/cbt-blk" | jq --exit-status '[.[] | select(.type == 1) | [.offset, .length]] == [[196608, 65536]]'
  _nbd_wait
  lxc delete v1/s10b --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk,YES\ns10,root,YES')" ]

  sub_test "Every attached custom volume has its own bitmaps"
  # A second block volume, attached while the instance runs, gets its volume metadata image with
  # the first snapshot with a bitmap that covers it.
  lxc storage volume create "${pool}" cbt-data size=32MiB --type block
  lxc storage volume attach "${pool}" cbt-data v1
  data_uuid="$(lxc storage volume get "${pool}" cbt-data volatile.uuid)"
  data_dev="/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_cbt--data"
  for _ in $(seq 30); do
    lxc exec v1 -- test -e "${data_dev}" && break
    sleep 1
  done

  [ ! -e "${bitmaps_dir}/${data_uuid}.qcow2" ]
  lxc snapshot v1 m1 --bitmap --disk-volumes all-exclusive
  [ -e "${bitmaps_dir}/${data_uuid}.qcow2" ]
  [ "$(_bitmaps v1)" = "$(printf 'm1,cbt-blk,YES\nm1,cbt-data,YES\nm1,root,YES\ns10,cbt-blk,YES\ns10,root,YES')" ]

  # A guest write to one volume marks the bitmap of that volume only.
  lxc exec v1 -- dd if=/dev/urandom of="${data_dev}" bs=64K count=1 seek=2 conv=fsync status=none
  lxc snapshot v1 m2 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1/m2)" = "$(printf 'm1,cbt-blk,NO\nm1,cbt-data,NO\nm1,root,NO\ns10,cbt-blk,NO\ns10,root,NO')" ]
  _nbd_serve nbd v1/m2 --previous-snapshot-uuid "$(_snapshot_uuid v1/m1)"
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status '[.exports[] | {name: ."export-name", contexts}] | sort_by(.name) == [
    {"name": "cbt-blk", "contexts": ["base:allocation", "qemu:dirty-bitmap:m1"]},
    {"name": "cbt-data", "contexts": ["base:allocation", "qemu:dirty-bitmap:m1"]},
    {"name": "root", "contexts": ["base:allocation", "qemu:dirty-bitmap:m1"]}]'
  _nbd_wait
  _nbd_serve nbd v1/m2 --previous-snapshot-uuid "$(_snapshot_uuid v1/m1)"
  nbdinfo --json --map=qemu:dirty-bitmap:m1 "${NBD_URI}/cbt-data" | jq --exit-status '[.[] | select(.type == 1) | [.offset, .length]] == [[131072, 65536]]'
  _nbd_wait
  _nbd_serve nbd v1/m2 --previous-snapshot-uuid "$(_snapshot_uuid v1/m1)"
  nbdinfo --json --map=qemu:dirty-bitmap:m1 "${NBD_URI}/cbt-blk" | jq --exit-status 'all(.[]; .type == 0)'
  _nbd_wait

  # The devices filter and the previous snapshot UUID apply together.
  _nbd_serve nbd v1/m2 --devices cbt-data --previous-snapshot-uuid "$(_snapshot_uuid v1/m1)"
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status '(.exports | length) == 1 and .exports[0]."export-name" == "cbt-data" and .exports[0].contexts == ["base:allocation", "qemu:dirty-bitmap:m1"]'
  _nbd_wait
  _nbd_serve nbd v1/m2 --devices root,cbt-data --previous-snapshot-uuid "$(_snapshot_uuid v1/m2)"
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status '[.exports[] | {name: ."export-name", contexts}] | sort_by(.name) == [
    {"name": "cbt-data", "contexts": ["base:allocation"]},
    {"name": "root", "contexts": ["base:allocation"]}]'
  _nbd_wait

  # A volume detached from the running instance loses its bitmaps and its volume metadata image.
  lxc delete v1/m2 v1/m1
  lxc storage volume detach "${pool}" cbt-data v1
  [ ! -e "${bitmaps_dir}/${data_uuid}.qcow2" ]
  lxc storage volume delete "${pool}" cbt-data
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk,YES\ns10,root,YES')" ]

  sub_test "Containers and stopped virtual machines get no bitmaps, and a container snapshot has no bitmap listing and no export"
  ensure_import_testimage
  lxc launch testimage c1
  [ "$(! "${_LXC}" snapshot c1 --bitmap 2>&1 1>/dev/null)" = "Error: A snapshot with a bitmap requires a running virtual machine" ]
  lxc snapshot c1 c1s
  [ "$(! "${_LXC}" bitmap list c1/c1s 2>&1 1>/dev/null)" = "Error: Dirty bitmaps are not supported for containers" ]
  [ "$(! "${_LXC}" bitmap show c1/c1s c1s 2>&1 1>/dev/null)" = "Error: Dirty bitmaps are not supported for containers" ]
  [ "$(! "${_LXC}" query /internal/instances/c1/bitmaps 2>&1 1>/dev/null)" = "Error: Dirty bitmaps are not supported for containers" ]
  [[ "$(_nbd_refused nbd c1/c1s)" == "Error: NBD export is only supported for virtual machines"* ]]
  lxc delete -f c1

  lxc stop -f v1
  [ "$(! "${_LXC}" snapshot v1 --bitmap 2>&1 1>/dev/null)" = "Error: A snapshot with a bitmap requires a running virtual machine" ]
  lxc snapshot v1 p2
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk,YES\ns10,root,YES')" ]
  lxc delete v1/p2
  lxc start v1
  waitInstanceReady v1

  sub_test "A shared volume has no bitmap and no export, and is left out of the snapshot while another instance uses it"
  # Attached to v1 alone, a shared volume is snapshotted with the instance, without a bitmap or an image.
  lxc storage volume create "${pool}" cbt-shared size=32MiB --type block security.shared=true
  lxc storage volume attach "${pool}" cbt-shared v1
  shared_uuid="$(lxc storage volume get "${pool}" cbt-shared volatile.uuid)"
  lxc snapshot v1 s11 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's10,cbt-blk,YES\ns10,root,YES\ns11,cbt-blk,YES\ns11,root,YES')" ]
  [ ! -e "${bitmaps_dir}/${shared_uuid}.qcow2" ]
  lxc config show v1/s11 | yq -r --exit-status '.config."volatile.attached_volumes"' | jq --exit-status 'has("cbt-blk") and has("cbt-shared")'
  [ "$(lxc query "/1.0/storage-pools/${pool}/volumes/custom/cbt-shared/snapshots" | jq --exit-status 'length')" = "1" ]
  _nbd_serve nbd v1/s11
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status '[.exports[]."export-name"] | sort == ["cbt-blk", "root"]'
  _nbd_wait
  [[ "$(_nbd_refused nbd v1/s11 --devices cbt-shared)" == 'Error: Snapshot has no volume snapshot with bitmaps for device "cbt-shared"'* ]]
  [[ "$(_nbd_refused storage volume nbd "${pool}" cbt-shared --writable)" == "Error: NBD export is not supported for shared volumes"* ]]

  # Attached to another instance as well, the volume is not snapshotted.
  lxc init --empty v2 --vm
  lxc storage volume attach "${pool}" cbt-shared v2
  lxc snapshot v1 s11b --bitmap --disk-volumes all-exclusive
  lxc config show v1/s11b | yq -r --exit-status '.config."volatile.attached_volumes"' | jq --exit-status 'has("cbt-blk") and (has("cbt-shared") | not)'
  [ "$(lxc query "/1.0/storage-pools/${pool}/volumes/custom/cbt-shared/snapshots" | jq --exit-status 'length')" = "1" ]
  _nbd_serve nbd v1/s11b
  nbdinfo --list --json "${NBD_URI}" | jq --exit-status '[.exports[]."export-name"] | sort == ["cbt-blk", "root"]'
  _nbd_wait
  lxc delete v1/s11b
  lxc delete v2
  lxc storage volume detach "${pool}" cbt-shared v1
  lxc storage volume delete "${pool}" cbt-shared

  sub_test "Enabling security.shared deletes the bitmaps of the volume"
  # The volume metadata image of the volume is deleted with its bitmaps on the running instance too.
  lxc storage volume set "${pool}" cbt-blk security.shared=true
  [ "$(_bitmaps v1)" = "$(printf 's10,root,YES\ns11,root,YES')" ]
  [ ! -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]

  # The copies in an earlier snapshot recorded the writes while the volume was exclusive, and are kept.
  [ "$(_bitmaps v1/s11)" = "$(printf 's10,cbt-blk,NO\ns10,root,NO')" ]
  [ "$(_nbd_contexts cbt-blk nbd v1/s11)" = '["base:allocation","qemu:dirty-bitmap:s10"]' ]
  lxc storage volume unset "${pool}" cbt-blk security.shared
  lxc snapshot v1 s12 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's10,root,YES\ns11,root,YES\ns12,cbt-blk,YES\ns12,root,YES')" ]

  # On a stopped instance the volume metadata image of the volume is deleted with its bitmaps as
  # well, and the next snapshot with a bitmap creates it again.
  # The config volume of a stopped instance is not mounted on every backend.
  # Therefore, the listing after the start shows it.
  lxc stop -f v1
  lxc storage volume set "${pool}" cbt-blk security.shared=true
  [ "$(_bitmaps v1)" = "$(printf 's10,root,YES\ns11,root,YES\ns12,root,YES')" ]
  lxc storage volume unset "${pool}" cbt-blk security.shared
  lxc start v1
  waitInstanceReady v1
  [ ! -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  [ "$(_bitmaps v1)" = "$(printf 's10,root,YES\ns11,root,YES\ns12,root,YES')" ]
  lxc delete v1/s12
  lxc snapshot v1 s12 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's10,root,YES\ns11,root,YES\ns12,cbt-blk,YES\ns12,root,YES')" ]

  sub_test "A writable NBD export of a stopped volume deletes its bitmaps"
  lxc stop -f v1
  [ "$(_bitmaps v1)" = "$(printf 's10,root,YES\ns11,root,YES\ns12,cbt-blk,YES\ns12,root,YES')" ]

  # The command requires --writable to confirm the read-write export.
  [ "$(! "${_LXC}" storage volume nbd "${pool}" cbt-blk 2>&1 1>/dev/null)" = "Error: The volume is served read-write, which --writable confirms" ]

  # A filesystem volume and a volume snapshot have no writable export.
  lxc storage volume create "${pool}" cbt-fs size=32MiB
  [[ "$(_nbd_refused storage volume nbd "${pool}" cbt-fs --writable)" == "Error: NBD export is only supported for block volumes"* ]]
  lxc storage volume delete "${pool}" cbt-fs
  blk_snap="cbt-blk/$(_volume_snapshot_of v1/s12)"
  [ "$(! "${_LXC}" storage volume nbd "${pool}" "${blk_snap}" --writable 2>&1 1>/dev/null)" = "Error: Invalid storage volume \"${blk_snap}\"" ]

  import_src="$(mktemp -p "${TEST_DIR}" import_src.XXX)"
  head -c "${blk_size}" /dev/urandom > "${import_src}"
  # The volume command listens on a given unix socket as the snapshot command does.
  _nbd_serve storage volume nbd "${pool}" cbt-blk --writable --address "${TEST_DIR}/nbd.sock"
  nbdinfo --json "nbd+unix:///?socket=${TEST_DIR}/nbd.sock" | jq --exit-status --argjson size "${blk_size}" '.exports[0] | (.is_read_only == false) and (."export-size" == $size)'
  _nbd_wait
  _nbd_serve storage volume nbd "${pool}" cbt-blk --writable
  nbdcopy --connections=1 "${import_src}" "${NBD_URI}"
  _nbd_wait
  [ "$(_bitmaps v1)" = "$(printf 's10,root,YES\ns11,root,YES\ns12,root,YES')" ]

  _nbd_serve storage volume nbd "${pool}" virtual-machine/v1 --writable
  nbdcopy --connections=1 "${s1_copy}" "${NBD_URI}"
  _nbd_wait
  rm -f "${s1_copy}"
  [ "$(_bitmaps v1 || echo fail)" = "" ]

  sub_test "A writable session refuses a second session, a start, a snapshot rename and enabling security.shared"
  # The conflict names the operation of the session.
  _nbd_serve storage volume nbd "${pool}" cbt-blk --writable
  _nbd_hold
  conflict="Error: Operation \"$(_nbd_sessions)\" (Importing storage volume over NBD) is already running for volume \"${pool}/cbt-blk\""
  [[ "$(_nbd_refused storage volume nbd "${pool}" cbt-blk --writable)" == "${conflict}"* ]]
  [[ "$(! "${_LXC}" start v1 2>&1 1>/dev/null)" == "${conflict}"* ]]
  [ "$(! "${_LXC}" storage volume set "${pool}" cbt-blk security.shared=true 2>&1 1>/dev/null)" = "${conflict}" ]
  _nbd_release

  # The start of a new instance takes the lock in the driver, so a create with the volume attached is refused too.
  lxc storage volume create "${pool}" cbt-new size=32MiB --type block
  _nbd_serve storage volume nbd "${pool}" cbt-new --writable
  _nbd_hold
  conflict="Error: Operation \"$(_nbd_sessions)\" (Importing storage volume over NBD) is already running for volume \"${pool}/cbt-new\""
  [[ "$(! "${_LXC}" launch --empty v4 --vm 2>&1 1>/dev/null <<< "devices: {data: {type: disk, source: cbt-new, pool: ${pool}}}")" == "${conflict}"* ]]
  _nbd_release
  lxc delete v4
  lxc storage volume delete "${pool}" cbt-new

  # A session of the root volume and a snapshot rename both delete bitmaps of the root disk.
  _nbd_serve storage volume nbd "${pool}" virtual-machine/v1 --writable
  _nbd_hold
  conflict="Error: Operation \"$(_nbd_sessions)\" (Importing storage volume over NBD) is already running for instance \"v1\""
  [[ "$(_nbd_refused storage volume nbd "${pool}" virtual-machine/v1 --writable)" == "${conflict}"* ]]
  [[ "$(! "${_LXC}" start v1 2>&1 1>/dev/null)" == "${conflict}"* ]]
  [ "$(! "${_LXC}" move v1/s10 v1/s10-renamed 2>&1 1>/dev/null)" = "${conflict}" ]
  [ "$(! "${_LXC}" storage volume set "${pool}" virtual-machine/v1 security.shared=true 2>&1 1>/dev/null)" = "${conflict}" ]
  _nbd_release

  sub_test "The server of a writable export runs under its own AppArmor profile, which is deleted with the session"
  _nbd_serve storage volume nbd "${pool}" cbt-blk --writable
  _nbd_hold
  _nbd_confined qemu-nbd
  _nbd_release
  [ -z "$(find "${LXD_DIR}/security/apparmor/profiles" -name 'lxd_qemu-nbd-*' || echo fail)" ]

  sub_test "The instance starts from the volumes that the writable exports wrote, and a running instance has no writable export"
  lxc start v1
  waitInstanceReady v1
  ! lxc exec v1 -- test -e /root/cbt.bin || false
  [ "$(lxc exec v1 -- sha256sum "${blk_dev}" | cut -d' ' -f1)" = "$(sha256sum "${import_src}" | cut -d' ' -f1)" ]
  rm -f "${import_src}"

  [[ "$(_nbd_refused storage volume nbd "${pool}" virtual-machine/v1 --writable)" == "Error: NBD export requires the instance to be stopped"* ]]
  [[ "$(_nbd_refused storage volume nbd "${pool}" cbt-blk --writable)" == 'Error: NBD export requires instance "v1" to be stopped'* ]]
  [ "$(! "${_LXC}" nbd v1 2>&1 1>/dev/null)" = "Error: Missing instance snapshot name" ]

  sub_test "A copy and an imported instance have no bitmaps"
  # The copy includes every snapshot of v1.
  # The snapshots that the remaining tests do not use are deleted first.
  lxc delete v1/s3 v1/s4 v1/s10 v1/s11 v1/s12 --disk-volumes all-exclusive
  lxc snapshot v1 s13 --bitmap
  [ "$(_bitmaps v1)" = "s13,root,YES" ]
  lxc stop -f v1

  # A block volume that is not shared is attached to one instance only.
  # It is therefore detached for the copies.
  lxc storage volume detach "${pool}" cbt-blk v1

  # The metadata images of v1 record neither the writes to v2 nor its snapshots.
  # Therefore, v2 starts without images.
  lxc copy v1 v2
  lxc start v2
  waitInstanceReady v2
  [ ! -e "${LXD_DIR}/virtual-machines/v2/bitmaps" ]
  [ "$(_bitmaps v2 || echo fail)" = "" ]
  lxc delete -f v2

  # The copy and the export kept the metadata images of v1.
  lxc storage volume attach "${pool}" cbt-blk v1
  lxc start v1
  waitInstanceReady v1
  [ "$(_bitmaps v1)" = "s13,root,YES" ]

  sub_test "A resize and a detach delete the bitmaps of the volume"
  lxc snapshot v1 s14 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's13,root,YES\ns14,cbt-blk,YES\ns14,root,YES')" ]
  lxc stop -f v1
  lxc storage volume set "${pool}" cbt-blk size=64MiB
  [ "$(_bitmaps v1)" = "$(printf 's13,root,YES\ns14,root,YES')" ]
  lxc config device set v1 root size=5GiB
  [ "$(_bitmaps v1 || echo fail)" = "" ]
  lxc start v1
  waitInstanceReady v1

  # The images are deleted with the bitmaps, and the next snapshot with a bitmap creates them again
  # at the new sizes, without preallocated tables.
  [ "$(lxc exec v1 -- blockdev --getsize64 "${blk_dev}")" = "$((64 * 1024 * 1024))" ]
  [ ! -e "${bitmaps_dir}/${root_uuid}.qcow2" ]
  [ ! -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  lxc snapshot v1 s15 --bitmap --disk-volumes all-exclusive
  [ "$(_bitmaps v1)" = "$(printf 's15,cbt-blk,YES\ns15,root,YES')" ]
  _metadata_image_info "${bitmaps_dir}/${root_uuid}.qcow2" | jq --exit-status '.raw == false and .size < 1048576 and .bitmaps == [{"name": "s15", "flags": ["in-use"]}]'
  lxc stop -f v1
  [ "$(_bitmaps v1)" = "$(printf 's15,cbt-blk,YES\ns15,root,YES')" ]
  lxc storage volume detach "${pool}" cbt-blk v1
  [ ! -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  [ "$(_bitmaps v1)" = "s15,root,YES" ]

  # A volume attached again starts without bitmaps and without an image.
  lxc storage volume attach "${pool}" cbt-blk v1
  lxc start v1
  waitInstanceReady v1
  [ ! -e "${bitmaps_dir}/${blk_uuid}.qcow2" ]
  [ "$(_bitmaps v1)" = "s15,root,YES" ]

  sub_test "A snapshot with a bitmap that covers an attached volume can be copied"
  # The config volume snapshot holds the overlay of the attached volume, which has guest writes
  # made after the snapshot. The copy leaves it alone, as only the instance commits its overlays.
  # A block volume that is not shared is attached to one instance only.
  # It is therefore detached from v1 for the copy, which has the devices of the snapshot.
  lxc snapshot v1 s16 --bitmap --disk-volumes all-exclusive
  lxc storage volume detach "${pool}" cbt-blk v1
  lxc copy v1/s16 v2
  lxc delete v2
  lxc delete -f v1

  sub_test "A stateful stop keeps the writes of the run in the bitmaps"
  # QEMU writes the store bitmaps into the images once the state of the guest is saved.
  # The write of the run is therefore merged into them before that.
  # A virtual machine with migration.stateful attaches no custom volume of a local pool.
  # The write is therefore made to the root disk, and the bitmap is checked for the block of the file only.
  lxc init ubuntu-vm v3 --vm -c migration.stateful=true -c limits.memory=384MiB -d root,size.state=384MiB -d "${SMALL_VM_ROOT_DISK}"
  lxc start v3
  waitInstanceReady v3
  lxc snapshot v3 s1 --bitmap
  lxc exec v3 -- sh -c 'head -c 65536 /dev/urandom > /root/cbt-stateful.bin && sync'
  file_offset="$(_file_offset v3 /root/cbt-stateful.bin)"
  lxc stop --stateful v3

  # The process that restores the state loads the store bitmaps only after the state is restored.
  # The start therefore creates the disk bitmaps from the bitmaps that the image lists.
  lxc start v3
  waitInstanceReady v3
  [ "$(_bitmaps v3)" = "s1,root,YES" ]
  lxc snapshot v3 s2 --bitmap
  _nbd_serve nbd v3/s2 --previous-snapshot-uuid "$(_snapshot_uuid v3/s1)"
  nbdinfo --json --map=qemu:dirty-bitmap:s1 "${NBD_URI}/root" | jq --exit-status --argjson offset "${file_offset}" 'any(.[]; .type == 1 and .offset <= $offset and $offset < .offset + .length)'
  _nbd_wait

  # A stateful snapshot saves the state the same way, and the guest that resumes loads the store bitmaps again.
  lxc snapshot v3 s3 --stateful
  [ "$(_bitmaps v3)" = "$(printf 's1,root,YES\ns2,root,YES')" ]

  # A start that discards the saved state loads the bitmaps as a start after a stop does.
  lxc exec v3 -- sh -c 'head -c 65536 /dev/urandom > /root/cbt-stateless.bin && sync'
  file_offset="$(_file_offset v3 /root/cbt-stateless.bin)"
  lxc stop --stateful v3
  lxc start v3 --stateless
  waitInstanceReady v3
  [ "$(_bitmaps v3)" = "$(printf 's1,root,YES\ns2,root,YES')" ]
  lxc snapshot v3 s4 --bitmap
  _nbd_serve nbd v3/s4 --previous-snapshot-uuid "$(_snapshot_uuid v3/s2)"
  nbdinfo --json --map=qemu:dirty-bitmap:s2 "${NBD_URI}/root" | jq --exit-status --argjson offset "${file_offset}" 'any(.[]; .type == 1 and .offset <= $offset and $offset < .offset + .length)'
  _nbd_wait

  # Cleanup.
  rm -f "${TEST_DIR}"/nbd_stderr.*
  lxc delete -f v3
  lxc storage volume delete "${pool}" cbt-blk

  if [ -n "${orig_volume_size:-}" ]; then
    # Restore the volume.size.
    lxc storage set "${pool}" volume.size "${orig_volume_size}"
  fi
}

# test_clustering_storage_block_tracking_vm creates a virtual machine on the first member of a two member cluster.
# It requests the NBD exports of the instance through the second member, which forwards them to the first.
test_clustering_storage_block_tracking_vm() {
  local poolDriver pool cert blk_dev blk_checksum blk_copy s1_uuid operation_uuid import_src fingerprint
  poolDriver="$(storage_backend "${LXD_INITIAL_DIR}")"
  if [ "${poolDriver}" = "dir" ]; then
    export TEST_UNMET_REQUIREMENT="Changed block tracking is not tested on the ${poolDriver} backend"
    return
  fi

  check_dependencies nbdinfo nbdcopy qemu-img qemu-nbd qemu-storage-daemon

  spawn_lxd_and_bootstrap_cluster "${poolDriver}"
  cert="$(cert_to_yaml "${LXD_ONE_DIR}/cluster.crt")"
  spawn_lxd_and_join_cluster "${cert}" 2 1 "${LXD_ONE_DIR}" "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" ensure_import_ubuntu_vm_image

  # The cluster is bootstrapped with the storage pool "data".
  pool="data"

  # Override the volume.size to accommodate a VM.
  LXD_DIR="${LXD_ONE_DIR}" lxc storage set "${pool}" volume.size="${SMALLEST_VM_ROOT_DISK}"

  LXD_DIR="${LXD_ONE_DIR}" lxc init ubuntu-vm v1 --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}" --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume create "${pool}" cbt-blk size=32MiB --type block
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume attach "${pool}" cbt-blk v1
  LXD_DIR="${LXD_ONE_DIR}" lxc start v1
  LXD_DIR="${LXD_ONE_DIR}" waitInstanceReady v1

  sub_test "The member of the instance serves the bitmap listing and the export of a snapshot requested through another member"
  # The guest never writes to the custom block volume on its own.
  # The write between the snapshots is therefore the only dirty block of the volume.
  blk_dev="/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_lxd_cbt--blk"
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot v1 s1 --bitmap --disk-volumes all-exclusive
  LXD_DIR="${LXD_ONE_DIR}" lxc exec v1 -- dd if=/dev/urandom of="${blk_dev}" bs=64K count=1 seek=1 conv=fsync
  LXD_DIR="${LXD_ONE_DIR}" lxc snapshot v1 s2 --bitmap --disk-volumes all-exclusive
  blk_checksum="$(LXD_DIR="${LXD_ONE_DIR}" lxc exec v1 -- sha256sum "${blk_dev}" | cut -d' ' -f1)"
  s1_uuid="$(LXD_DIR="${LXD_ONE_DIR}" _snapshot_uuid v1/s1)"
  [ "$(LXD_DIR="${LXD_TWO_DIR}" _bitmaps v1/s2)" = "$(printf 's1,cbt-blk,NO\ns1,root,NO')" ]

  LXD_DIR="${LXD_TWO_DIR}" _nbd_serve nbd v1/s2 --previous-snapshot-uuid "${s1_uuid}"
  nbdinfo --json --map=qemu:dirty-bitmap:s1 "${NBD_URI}/cbt-blk" | jq --exit-status '[.[] | select(.type == 1) | [.offset, .length]] == [[65536, 65536]]'
  LXD_DIR="${LXD_TWO_DIR}" _nbd_wait

  blk_copy="$(mktemp -p "${TEST_DIR}" blk_copy.XXX)"
  LXD_DIR="${LXD_TWO_DIR}" _nbd_serve nbd v1/s2
  nbdcopy --connections=1 "${NBD_URI}/cbt-blk" "${blk_copy}"
  LXD_DIR="${LXD_TWO_DIR}" _nbd_wait
  [ "$(sha256sum "${blk_copy}" | cut -d' ' -f1)" = "${blk_checksum}" ]

  sub_test "The export runs as an operation on the member of the instance, which another member cancels"
  LXD_DIR="${LXD_TWO_DIR}" _nbd_serve nbd v1/s2
  LXD_DIR="${LXD_TWO_DIR}" _nbd_hold
  [ "$(LXD_DIR="${LXD_TWO_DIR}" lxc operation list --format json | jq --exit-status --compact-output '[.[] | select(.description == "Exporting instance snapshot over NBD" and .status == "Running") | .location]')" = '["node1"]' ]
  operation_uuid="$(LXD_DIR="${LXD_TWO_DIR}" _nbd_sessions)"
  LXD_DIR="${LXD_TWO_DIR}" lxc operation delete "${operation_uuid}"

  # The holder exits once the member of the instance closes the connection.
  for _ in $(seq 60); do
    kill -0 "${NBD_HOLDER_PID}" 2>/dev/null || break
    sleep 0.5
  done

  ! kill -0 "${NBD_HOLDER_PID}" 2>/dev/null || false
  LXD_DIR="${LXD_TWO_DIR}" _nbd_release

  # The member that forwards the request passes on the operation URL of the member of the instance.
  LXD_DIR="${LXD_TWO_DIR}" _nbd_check_location GET /1.0/instances/v1/snapshots/s2/nbd

  sub_test "The member of the instance serves a writable export requested through another member"
  LXD_DIR="${LXD_ONE_DIR}" lxc stop -f v1
  import_src="$(mktemp -p "${TEST_DIR}" import_src.XXX)"
  head -c "$((32 * 1024 * 1024))" /dev/urandom > "${import_src}"
  LXD_DIR="${LXD_TWO_DIR}" _nbd_serve storage volume nbd "${pool}" cbt-blk --writable
  nbdcopy --connections=1 "${import_src}" "${NBD_URI}"
  LXD_DIR="${LXD_TWO_DIR}" _nbd_wait
  [ "$(LXD_DIR="${LXD_ONE_DIR}" _bitmaps v1)" = "$(printf 's1,root,YES\ns2,root,YES')" ]

  # The member of the instance reads back what the forwarded export wrote.
  LXD_DIR="${LXD_ONE_DIR}" _nbd_serve storage volume nbd "${pool}" cbt-blk --writable
  nbdcopy --connections=1 "${NBD_URI}" "${blk_copy}"
  LXD_DIR="${LXD_ONE_DIR}" _nbd_wait
  [ "$(sha256sum "${blk_copy}" | cut -d' ' -f1)" = "$(sha256sum "${import_src}" | cut -d' ' -f1)" ]
  LXD_DIR="${LXD_TWO_DIR}" _nbd_check_location POST "/1.0/storage-pools/${pool}/volumes/custom/cbt-blk/nbd"

  LXD_DIR="${LXD_TWO_DIR}" _nbd_serve storage volume nbd "${pool}" virtual-machine/v1 --writable
  nbdinfo --json "${NBD_URI}" | jq --exit-status '.exports[0].is_read_only == false'
  LXD_DIR="${LXD_TWO_DIR}" _nbd_wait
  [ "$(LXD_DIR="${LXD_ONE_DIR}" _bitmaps v1 || echo fail)" = "" ]

  # Cleanup.
  rm -f "${blk_copy}" "${import_src}" "${TEST_DIR}"/nbd_stderr.*
  fingerprint="$(LXD_DIR="${LXD_ONE_DIR}" lxc config get v1 volatile.base_image)"
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f v1
  LXD_DIR="${LXD_ONE_DIR}" lxc storage volume delete "${pool}" cbt-blk
  LXD_DIR="${LXD_ONE_DIR}" lxc image delete "${fingerprint}"

  # The root disk of the default profile uses the pool, and a pool in use cannot be deleted.
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete "${pool}"

  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown

  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
}
