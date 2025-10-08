# LXD-related test helpers.

spawn_lxd() {
    { set +x; } 2>/dev/null
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.
    local LXD_DIR="${1}"
    shift
    chmod +x "${LXD_DIR}"

    local storage="${1}"
    shift

    # shellcheck disable=SC2153
    local lxd_backend="${LXD_BACKEND}"
    if [ "$LXD_BACKEND" = "random" ]; then
        lxd_backend="$(random_storage_backend)"
    fi

    if [ "${lxd_backend}" = "ceph" ] && [ -z "${LXD_CEPH_CLUSTER:-}" ]; then
        echo "A cluster name must be specified when using the CEPH driver." >&2
        exit 1
    fi

    # setup storage
    "$lxd_backend"_setup "${LXD_DIR}"
    echo "$lxd_backend" > "${LXD_DIR}/lxd.backend"

    echo "==> Spawning lxd in ${LXD_DIR}"

    if [ "${LXD_NETNS}" = "" ]; then
        lxd --logfile "${LXD_DIR}/lxd.log" "${SERVER_DEBUG-}" "$@" 2>&1 &
    else
        # shellcheck disable=SC2153
        read -r pid < "${TEST_DIR}/ns/${LXD_NETNS}/PID"
        nsenter -n -m -t "${pid}" lxd --logfile "${LXD_DIR}/lxd.log" "${SERVER_DEBUG-}" "$@" 2>&1 &
    fi
    local LXD_PID=$!
    echo "${LXD_PID}" > "${LXD_DIR}/lxd.pid"
    # shellcheck disable=SC2153
    echo "${LXD_DIR}" >> "${TEST_DIR}/daemons"
    echo "==> Spawned LXD (PID is ${LXD_PID})"

    echo "==> Confirming lxd is responsive (PID is ${LXD_PID})"
    lxd waitready --timeout=300 || (echo "Killing PID ${LXD_PID}" ; kill -9 "${LXD_PID}" ; false)

    if [ "${LXD_NETNS}" = "" ]; then
        echo "==> Binding to network"
        for _ in $(seq 10); do
            addr="127.0.0.1:$(local_tcp_port)"
            lxc config set core.https_address "${addr}" || continue
            echo "${addr}" > "${LXD_DIR}/lxd.addr"
            echo "==> Bound to ${addr}"
            break
        done
    fi

    echo "==> Setting trust password"
    LXD_DIR="${lxddir}" lxc config set core.trust_password foo
    if [ -n "${SHELL_TRACING:-}" ]; then
        set -x
    fi

    if [ "${LXD_NETNS}" = "" ]; then
        echo "==> Setting up networking"
        lxc profile device add default eth0 nic nictype=p2p name=eth0
    fi

    if [ "${storage}" = true ]; then
        echo "==> Configuring storage backend"
        "$lxd_backend"_configure "${LXD_DIR}"
    fi
}

respawn_lxd() {
    { set +x; } 2>/dev/null
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.
    local LXD_DIR="${1}"
    shift
    local wait="${1}"
    shift

    echo "==> Spawning lxd in ${LXD_DIR}"
    if [ "${LXD_NETNS}" = "" ]; then
        lxd --logfile "${LXD_DIR}/lxd.log" "${SERVER_DEBUG-}" "$@" 2>&1 &
    else
        read -r pid < "${TEST_DIR}/ns/${LXD_NETNS}/PID"
        nsenter -n -m -t "${pid}" lxd --logfile "${LXD_DIR}/lxd.log" "${SERVER_DEBUG-}" "$@" 2>&1 &
    fi
    LXD_PID=$!
    echo "${LXD_PID}" > "${LXD_DIR}/lxd.pid"
    echo "==> Spawned LXD (PID is ${LXD_PID})"

    if [ "${wait}" = true ]; then
        echo "==> Confirming lxd is responsive (PID is ${LXD_PID})"
        lxd waitready --timeout=300 || (echo "Killing PID ${LXD_PID}" ; kill -9 "${LXD_PID}" ; false)
    fi

    if [ -n "${SHELL_TRACING:-}" ]; then
        set -x
    fi
}

kill_lxd() {
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.
    local LXD_DIR="${1}"
    shift

    # Check if already killed
    if [ ! -f "${LXD_DIR}/lxd.pid" ]; then
      return
    fi

    local LXD_PID lxd_backend check_leftovers
    LXD_PID=$(< "${LXD_DIR}/lxd.pid")
    check_leftovers="false"
    lxd_backend="$(storage_backend "${LXD_DIR}")"
    echo "==> Killing LXD at ${LXD_DIR} (${LXD_PID})"

    if [ -e "${LXD_DIR}/unix.socket" ]; then
        # Delete all containers
        echo "==> Deleting all instances from all projects"
        timeout -k 2 2 lxc list --force-local --all-projects --format csv --columns ne | sed 's/,/ /g' | while read -r instance project; do
            echo "==> Deleting instance ${instance} from project ${project}"
            timeout -k 10 10 lxc delete "${instance}" --project "${project}" --force-local -f || true
        done

        # Delete all images
        echo "==> Deleting all images from all projects"
        timeout -k 2 2 lxc image list --force-local --format csv --columns Fe | sed 's/,/ /g' | while read -r image project; do
            timeout -k 10 10 lxc image delete "${image}" --project "${project}" --force-local || true
        done

        # Delete all profiles
        echo "==> Deleting all profiles"
        for profile in $(timeout -k 2 2 lxc profile list --force-local --format csv --columns n); do
            # default cannot be deleted.
            [ "${profile}" = "default" ] && continue
            timeout -k 10 10 lxc profile delete "${profile}" --force-local || true
        done

        # Delete all networks
        echo "==> Deleting all managed networks"
        for network in $(timeout -k 2 2 lxc network list --force-local --format csv | awk -F, '{if ($3 == "YES") {print $1}}'); do
            timeout -k 10 10 lxc network delete "${network}" --force-local || true
        done

        # Clear config of the default profile since the profile itself cannot
        # be deleted.
        echo "==> Clearing config of default profile"
        printf 'config: {}\ndevices: {}' | timeout -k 5 5 lxc profile edit default

        echo "==> Deleting all storage pools"
        path="/1.0/storage-pools"
        for storage_pool in $(lxc query "${path}" | jq --exit-status --raw-output ".[] | ltrimstr(\"${path}/\")"); do
            # Delete the storage volumes.
            path="/1.0/storage-pools/${storage_pool}/volumes/custom"
            for volume in $(lxc query "${path}" | jq --exit-status --raw-output ".[] | ltrimstr(\"${path}/\")"); do
                echo "==> Deleting storage volume ${volume} on ${storage_pool}"
                timeout -k 20 20 lxc storage volume delete "${storage_pool}" "${volume}" --force-local || true
            done

            # Delete the storage buckets.
            path="/1.0/storage-pools/${storage_pool}/buckets"
            for bucket in $(lxc query "${path}" | jq --exit-status --raw-output ".[] | ltrimstr(\"${path}/\")"); do
                echo "==> Deleting storage bucket ${bucket} on ${storage_pool}"
                timeout -k 20 20 lxc storage bucket delete "${storage_pool}" "${bucket}" --force-local || true
            done

            ## Delete the storage pool.
            timeout -k 20 20 lxc storage delete "${storage_pool}" --force-local || true
        done

        echo "==> Checking for locked DB tables"
        for table in $(echo .tables | sqlite3 "${LXD_DIR}/local.db"); do
            echo "SELECT 1 FROM ${table} LIMIT 1;" | sqlite3 "${LXD_DIR}/local.db" >/dev/null
        done

        # Kill the daemon
        timeout -k 30 30 lxd shutdown || kill -9 "${LXD_PID}" 2>/dev/null || true

        sleep 2

        # Cleanup devlxd and shmounts (needed due to the forceful kill)
        find "${LXD_DIR}" \( -name devlxd -o -name shmounts \) -exec "umount" "-q" "-l" "{}" + || true

        check_leftovers="true"
    fi

    # If SERVER_DEBUG is set, check for panics in the daemon logs
    if [ -n "${SERVER_DEBUG:-}" ]; then
      "${MAIN_DIR}/deps/panic-checker" "${LXD_DIR}/lxd.log"
    fi

    if [ -n "${LXD_LOGS:-}" ]; then
        echo "==> Copying the logs"
        mkdir -p "${LXD_LOGS}/${LXD_PID}"
        cp -R "${LXD_DIR}/logs/" "${LXD_LOGS}/${LXD_PID}/"
        cp "${LXD_DIR}/lxd.log" "${LXD_LOGS}/${LXD_PID}/"
    fi

    if [ "${check_leftovers}" = "true" ]; then
        echo "==> Checking for leftover files"
        rm -f "${LXD_DIR}/containers/lxc-monitord.log"

        # Support AppArmor policy cache directory
        apparmor_cache_dir="$(apparmor_parser --cache-loc "${LXD_DIR}"/security/apparmor/cache --print-cache-dir)"
        rm -f "${apparmor_cache_dir}/.features"
        check_empty "${LXD_DIR}/containers/"
        check_empty "${LXD_DIR}/devices/"
        check_empty "${LXD_DIR}/images/"
        # FIXME: Once container logging rework is done, uncomment
        # check_empty "${LXD_DIR}/logs/"
        check_empty "${apparmor_cache_dir}"
        check_empty "${LXD_DIR}/security/apparmor/profiles/"
        check_empty "${LXD_DIR}/security/seccomp/"
        check_empty "${LXD_DIR}/shmounts/"
        check_empty "${LXD_DIR}/snapshots/"

        echo "==> Checking for leftover DB entries"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "images"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "images_aliases"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "images_nodes"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "images_properties"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "images_source"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "instances"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "instances_config"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "instances_devices"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "instances_devices_config"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "instances_profiles"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "networks"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "networks_config"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "profiles"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "profiles_config"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "profiles_devices"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "profiles_devices_config"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "storage_pools"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "storage_pools_config"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "storage_pools_nodes"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "storage_volumes"
        check_empty_table "${LXD_DIR}/database/global/db.bin" "storage_volumes_config"
    fi

    # teardown storage
    "$lxd_backend"_teardown "${LXD_DIR}"

    # Wipe the daemon directory
    wipe "${LXD_DIR}"

    # Remove the daemon from the list
    sed "\\|^${LXD_DIR}|d" -i "${TEST_DIR}/daemons"
}

shutdown_lxd() {
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.
    local LXD_DIR="${1}"
    shift

    local LXD_PID
    LXD_PID=$(< "${LXD_DIR}/lxd.pid")
    echo "==> Shutting down LXD at ${LXD_DIR} (${LXD_PID})"

    # Shutting down the daemon
    lxd shutdown || kill -9 "${LXD_PID}" 2>/dev/null || true

    # Wait for any cleanup activity that might be happening right
    # after the websocket is closed.
    sleep 0.5
}

wait_for() {
    local addr op
    addr="${1}"
    shift
    op="$("$@" | jq --exit-status --raw-output '.operation')"
    my_curl "https://${addr}${op}/wait"
}

wipe() {
    if command -v btrfs >/dev/null 2>&1; then
        rm -Rf "${1}" 2>/dev/null || true
        if [ -d "${1}" ]; then
            find "${1}" | tac | xargs btrfs subvolume delete >/dev/null 2>&1 || true
        fi
    fi

    if mountpoint -q "${1}"; then
        umount -l "${1}"
    fi

    rm -Rf "${1}"
}

panic_checker() {
  # Only run if SERVER_DEBUG is set (e.g. LXD_VERBOSE or LXD_DEBUG is set)
  # Panics are logged at info level, which won't be outputted unless this is set.
  if [ -z "${SERVER_DEBUG:-}" ]; then
    return 0
  fi

  local test_dir daemon_dir
  test_dir="${1}"

  [ -s "${test_dir}/daemons" ] || return

  while read -r daemon_dir; do
    [ -s "${daemon_dir}/lxd.log" ] || continue
    "${MAIN_DIR}/deps/panic-checker" "${daemon_dir}/lxd.log"
  done < "${test_dir}/daemons"
}

# Kill and cleanup LXD instances and related resources
cleanup_lxds() {
    local test_dir daemon_dir
    test_dir="$1"

    # Kill all LXD instances
    if [ -s "${test_dir}/daemons" ]; then
      while read -r daemon_dir; do
          kill_lxd "${daemon_dir}"
      done < "${test_dir}/daemons"
    fi

    # Cleanup leftover networks
    # shellcheck disable=SC2009
    ps aux | grep "interface=lxdt$$ " | grep -v grep | awk '{print $2}' | while read -r line; do
        kill -9 "${line}"
    done
    if [ -e "/sys/class/net/lxdt$$" ]; then
        ip link del lxdt$$
    fi

    # Cleanup clustering networking, if any
    teardown_clustering_netns
    teardown_clustering_bridge

    # Wipe the test environment
    wipe "$test_dir"

    umount_loops "$test_dir"
}

lxd_shutdown_restart() {
    local scenario="${1}"
    local LXD_PID

    LXD_PID=$(< "${LXD_DIR}/lxd.pid")
    echo "==> Shutting down LXD at ${LXD_DIR} (${LXD_PID})"

    local logfile="${scenario}.log"
    echo "Starting LXD log capture in $logfile using lxc monitor..."
    lxc monitor --pretty > "$logfile" 2>&1 &
    local monitor_pid=$!

    # Give monitor a moment to connect
    sleep 2
    echo "Monitor PID: $monitor_pid"
    echo "LXD daemon PID: $LXD_PID"
    echo "Starting LXD shutdown sequence..."
    if ! kill -SIGPWR "$LXD_PID" 2>/dev/null; then
        echo "Failed to signal LXD to shutdown" | tee -a "$logfile"
        return 1
    fi

    echo "Waiting for LXD to shutdown gracefully..." | tee -a "$logfile"
    for _ in $(seq 540); do
        if ! kill -0 "${LXD_PID}" 2>/dev/null; then
            # The monitor process will terminate once LXD exits
            wait "${monitor_pid}" || true
            break
        fi
        sleep 0.5
    done

    echo "LXD shutdown sequence completed."
    respawn_lxd "${LXD_DIR}" true
}

# create_instances creates a specified number of instances in the background.
# The instance are called i1, i2, i3, etc.
create_instances() {
  local n="${1}"  # Number of instances to create.

  for i in $(seq "${n}"); do
    echo "Creating instance i${i}..."
    lxc launch --quiet testimage "i${i}" -d "${SMALL_ROOT_DISK}"
  done

  echo "All instances created successfully."
}
