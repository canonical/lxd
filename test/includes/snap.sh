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
# 3. holds the installed snap to prevent refreshes
install_snap() {
    local name="${1}"
    local channel="${2:-"latest/edge"}"
    local cache_dir="${SNAP_CACHE_DIR:-${HOME}/snap-cache}"
    local dir="${cache_dir}/${name}/${channel/\//-}"

    if snap list "${name}" >/dev/null 2>&1; then
        echo "Snap ${name} is already installed"
        return 0
    fi

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

        # Check if we're already in a recursive call by looking at the call stack
        local recursive_call=false
        local i
        for ((i=1; i<${#FUNCNAME[@]}; i++)); do
            if [[ "${FUNCNAME[${i}]}" == "install_snap" ]]; then
                recursive_call=true
                break
            fi
        done

        # If files are missing and we're not in a recursive call
        if { [ -z "${assert}" ] || ! [ -e "${snap}" ]; } && [ "${recursive_call}" = "false" ]; then
            echo "Opportunistically downloading ${name} before installation"
            if download_snap "${name}" "${channel}"; then
                exec install_snap "${name}" "${channel}"
            else
                echo "Error: Failed to download ${name} from channel ${channel}" >&2
                exit 1
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
        snap refresh --hold "${name}"
    )
}
