# lxc CLI related test helpers.

lxc() {
    { set +x; } 2>/dev/null
    LXC_LOCAL=1 lxc_remote "$@"
}

lxc_remote() {
    { set +x; } 2>/dev/null
    local cmd_args=()

    # Inject client debug flag if needed
    if [ -n "${CLIENT_DEBUG:-}" ]; then
        cmd_args+=("${CLIENT_DEBUG}")
    fi

    # Inject --force-local if needed
    # This is only done when "--" is present to avoid breaking commands that
    # rely on the config file (e.g. lxc project switch).
    if [ -n "${LXC_LOCAL:-}" ]; then
        local arg
        # Scan all args looking for "--" marker
        for arg in "$@"; do
            if [ "${arg}" = "--" ]; then
                cmd_args+=("--force-local")
                break
            fi
        done
    fi

    if [ -n "${SHELL_TRACING:-}" ]; then
        set -x
    fi

    # _LXC contains the path to the lxc binary
    timeout --foreground 120 "${_LXC}" "${cmd_args[@]}" "$@"
}
