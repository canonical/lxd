# lxc CLI related test helpers.

lxc() {
    { set +x; } 2>/dev/null
    LXC_LOCAL=1 lxc_remote "$@"
}

lxc_remote() {
    { set +x; } 2>/dev/null
    local injected cmd arg

    injected=0
    # _LXC contains the path to lxc binary, not the shell wrapper function
    cmd="${_LXC}"

    # shellcheck disable=SC2048,SC2068
    for arg in "$@"; do
        if [ "${arg}" = "--" ]; then
            injected=1
            cmd="${cmd} ${CLIENT_DEBUG:-}"
            [ -n "${LXC_LOCAL}" ] && cmd="${cmd} --force-local"
            cmd="${cmd} --"
        elif [ "${arg}" = "--force-local" ]; then
            continue
        else
            cmd="${cmd} \"${arg}\""
        fi
    done

    if [ "${injected}" = "0" ]; then
        cmd="${cmd} ${CLIENT_DEBUG-}"
    fi
    if [ -n "${SHELL_TRACING:-}" ]; then
        eval "set -x;timeout --foreground 120 ${cmd}"
    else
        eval "timeout --foreground 120 ${cmd}"
    fi
}
