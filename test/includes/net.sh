# Network-related helper functions.

# Return an available random local port
local_tcp_port() {
    if which python3 >/dev/null 2>&1; then
        (
            cat << EOF
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
EOF
        ) | python3
        return
    fi

    # shellcheck disable=SC2039
    local port pid

    while true; do
        port=$(shuf -i 10000-32768 -n 1)
        nc -l 127.0.0.1 "${port}" >/dev/null 2>&1 &
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
