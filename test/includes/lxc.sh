# lxc CLI related test helpers.

lxc() {
    LXC_LOCAL=1 lxc_remote "$@"
}

lxc_remote() {
    set +x
    # shellcheck disable=SC2039
    local injected cmd arg

    injected=0
    cmd=$(which lxc)

    # shellcheck disable=SC2048,SC2068
    for arg in "$@"; do
        if [ "${arg}" = "--" ]; then
            injected=1
            cmd="${cmd} ${DEBUG:-}"
            [ -n "${LXC_LOCAL}" ] && cmd="${cmd} --force-local"
            cmd="${cmd} --"
        elif [ "${arg}" = "--force-local" ]; then
            continue
        else
            cmd="${cmd} \"${arg}\""
        fi
    done

    if [ "${injected}" = "0" ]; then
        cmd="${cmd} ${DEBUG-}"
    fi
    if [ -n "${DEBUG:-}" ]; then
        set -x
    fi
    eval "${cmd}"
}

gen_cert() {
    # Temporarily move the existing cert to trick LXC into generating a
    # second cert.  LXC will only generate a cert when adding a remote
    # server with a HTTPS scheme.  The remote server URL just needs to
    # be syntactically correct to get past initial checks; in fact, we
    # don't want it to succeed, that way we don't have to delete it later.
    [ -f "${LXD_CONF}/${1}.crt" ] && return
    mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
    mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
    echo y | lxc_remote remote add "$(uuidgen)" https://0.0.0.0 || true
    mv "${LXD_CONF}/client.crt" "${LXD_CONF}/${1}.crt"
    mv "${LXD_CONF}/client.key" "${LXD_CONF}/${1}.key"
    mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
    mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"
}
