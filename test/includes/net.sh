# Network-related helper functions.

# Return an available random local port
local_tcp_port() {
    # shellcheck disable=SC2039
    local port pid

    while true; do
        port=$(shuf -i 10000-32768 -n 1)
        nc -l 127.0.0.1 "${port}" >/dev/null 2>&1 &
        sleep 1
        pid=$!
        kill "${pid}" >/dev/null 2>&1 || continue
        wait "${pid}" || true
        echo "${port}"
        return
    done
}

# Certificate-aware curl wrapper
my_curl() {
    curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" "$@"
}
