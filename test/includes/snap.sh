# download_snap: downloads a snap to the cache dir.
download_snap() {
    local name="${1}"
    local channel="${2:-"latest/edge"}"
    local cache_dir="${SNAP_CACHE_DIR:-${HOME}/snap-cache}"
    local dir="${cache_dir}/${name}/${channel/\//-}"

    [ -d "${dir}" ] || mkdir -p "${dir}"
    (
        set -eux
        cd "${dir}"
        # Delete any revs older than 1 day
        find . -type f -mtime +1 \( -name "${name}_*.snap" -o -name "${name}_*.assert" \) -delete
        snap download "${name}" --channel="${channel}" --cohort="+"
    )
}

# install_snap: installs a snap from the cache dir.
# The cache dir content should look like this:
# # ls -1
# lxd_35505.assert
# lxd_35505.snap
#
# 1. acknowledges the assertion
# 2. install the snap with the name prefix
# 3. holds (24h) the installed snap to prevent refreshes during test runs
install_snap() {
    local name="${1}"
    local channel="${2:-"latest/edge"}"
    local cache_dir="${SNAP_CACHE_DIR:-${HOME}/snap-cache}"
    local dir="${cache_dir}/${name}/${channel/\//-}"

    # Use process substitution (< <(...)) to avoid running the 'while' loop in
    # a subshell, which ensures 'return 0' can exit the install_snap function.
    local track
    while read -r _ _ _ track _ _ _; do
        # Ignore header
        [ "${track}" = "Tracking" ] && continue

        # If the snap was installed from a local file (track="-") or the one
        # requested, nothing left to do
        if [ "${track}" = "-" ] || [ "${track}" = "${channel}" ]; then
            return 0
        fi

        # The snap is installed but from the wrong track so proceed with the
        # installation
        break
    done < <(snap list "${name}" 2>/dev/null)

    [ -d "${dir}" ] || mkdir -p "${dir}"
    (
        local assert snap
        set -eux
        cd "${dir}"

        # Find the first matching .assert file, or leave empty if none found
        assert=""
        for f in ./"${name}"_*.assert; do
            if [ -e "$f" ]; then
                assert="$f"
                break
            fi
        done

        snap=""
        if [ -n "${assert}" ]; then
            snap="${assert/%.assert/.snap}"
        fi

        # If files are missing and we're not in a recursive call
        if [ -z "${assert}" ] || ! [ -e "${snap}" ]; then
            # Check if we're already in a recursive call by looking at the call stack
            local recursive_call=false
            local i
            for ((i=1; i<${#FUNCNAME[@]}; i++)); do
                if [[ "${FUNCNAME[${i}]}" == "install_snap" ]]; then
                    recursive_call=true
                    break
                fi
            done

            if [ "${recursive_call}" = "false" ]; then
              echo "Opportunistically downloading ${name} before installation"
              if download_snap "${name}" "${channel}"; then
                  install_snap "${name}" "${channel}"
                  return
              else
                  echo "Error: Failed to download ${name} from channel ${channel}" >&2
                  exit 1
              fi
            fi
        fi

        # Final check - if we still don't have the files, fail
        if [ -z "${assert}" ] || ! [ -e "${snap}" ]; then
            echo "Error: Required snap files not found in ${dir}" >&2
            echo "Expected: ${name}_*.assert and corresponding .snap file" >&2
            exit 1
        fi

        echo "Installing ${name} from cache"
        snap ack "${assert}"
        snap install "${snap}"
        snap refresh --hold=24h "${name}"
        snap switch "${name}" --channel="${channel}"
    )
}

# sideload_lxd_snap: installs the lxd snap and sideloads the lxc, lxd and lxd-agent binaries.
sideload_lxd_snap() {
    local channel="${1:-"latest/edge"}"
    local bin
    install_snap lxd "${channel}"

    for bin in "${_LXC}" "$(command -v lxd)"; do
        cp "${bin}" "/var/snap/lxd/common/${bin##*/}.debug"
    done

    # Use a mount bind as /snap/lxd is readonly
    mount -o ro,bind "$(command -v lxd-agent)" /snap/lxd/current/bin/lxd-agent
}
