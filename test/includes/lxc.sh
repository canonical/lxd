# lxc CLI related test helpers.

lxc() {
    { set +x; } 2>/dev/null
    LXC_LOCAL=1 lxc_remote "$@"
}

lxc_remote() {
    { set +x; } 2>/dev/null
    local injected arg
    local cmd_args=()

    injected=0
    # _LXC contains the path to the lxc binary
    cmd_args+=("${_LXC}")

    for arg in "$@"; do
        if [ "${arg}" = "--" ]; then
            injected=1
            [ -n "${CLIENT_DEBUG:-}" ] && cmd_args+=("${CLIENT_DEBUG}")
            [ -n "${LXC_LOCAL}" ] && cmd_args+=('--force-local')
            cmd_args+=('--')
        elif [ "${arg}" = "--force-local" ]; then
            continue
        else
            cmd_args+=("${arg}")
        fi
    done

    if [ "${injected}" = "0" ]; then
        [ -n "${CLIENT_DEBUG:-}" ] && cmd_args+=("${CLIENT_DEBUG}")
    fi

    if [ -n "${SHELL_TRACING:-}" ]; then
        set -x
    fi

    timeout --foreground 120 "${cmd_args[@]}"
}
