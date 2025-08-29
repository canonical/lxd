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
        return 1
    fi

    [ -d "${dir}" ] || mkdir -p "${dir}"
    (
        local assert snap
        set -eux
        cd "${dir}"
        assert="$(echo ./"${name}"_*.assert)"
        snap=./"${assert/%.assert/.snap}"

        if ! [ -e "${assert}" ] || ! [ -e "${snap}" ]; then
            echo "Opportunistically downloading ${name} before installation"
            download_snap "${name}" "${channel}"
            install_snap "${name}" "${channel}"
            return
        fi

        echo "Installing ${name} from cache"
        snap ack "${assert}"
        snap install "${snap}"
        snap refresh --hold "${name}"
    )
}
